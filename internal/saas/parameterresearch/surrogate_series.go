package parameterresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/parameterresearch"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) PlanSurrogate(ctx context.Context, userID, runID uint, req SurrogatePlanRequest) (computetask.PlanPreview, error) {
	_, configuration, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return computetask.PlanPreview{}, err
	}
	input, _, err := s.surrogateInput(ctx, runID, configuration.ID, canonical, req.Seed)
	if err != nil {
		return computetask.PlanPreview{}, err
	}
	raw, _ := compute.CanonicalJSON(input)
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "p10.parameter-research.surrogate", Title: "參數研究：代理模型", ExecutorType: SurrogateExecutorType, Settings: map[string]any{"configuration_id": configuration.ID, "run_id": runID, "schema_version": ConfigurationSchemaVersion}, ResearchSettingID: fmt.Sprintf("p10-configuration:%d", configuration.ID), ResearchSettingHash: configuration.ConfigHash, StageKey: "surrogate", StageType: "surrogate", Items: []compute.ManifestItemInput{{Key: "train", CacheKey: "p10-surrogate:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: int64(len(input.Examples) * input.Settings.Trees)}}}
	return s.computeTasks.Preview(ctx, userID, spec)
}

func (s *Service) StartSurrogate(ctx context.Context, userID, runID uint, req StartSurrogateRequest) (SurrogateDescriptor, error) {
	_, configuration, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return SurrogateDescriptor{}, err
	}
	input, pointSetHash, err := s.surrogateInput(ctx, runID, configuration.ID, canonical, req.Seed)
	if err != nil {
		return SurrogateDescriptor{}, err
	}
	raw, _ := compute.CanonicalJSON(input)
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "p10.parameter-research.surrogate", Title: "參數研究：代理模型", ExecutorType: SurrogateExecutorType, Settings: map[string]any{"configuration_id": configuration.ID, "run_id": runID, "schema_version": ConfigurationSchemaVersion}, ResearchSettingID: fmt.Sprintf("p10-configuration:%d", configuration.ID), ResearchSettingHash: configuration.ConfigHash, StageKey: "surrogate", StageType: "surrogate", Items: []compute.ManifestItemInput{{Key: "train", CacheKey: "p10-surrogate:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: int64(len(input.Examples) * input.Settings.Trees)}}}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return SurrogateDescriptor{}, err
	}
	if preview.PlanKey != req.PlanKey {
		return SurrogateDescriptor{}, ErrPlanStale
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		return SurrogateDescriptor{}, err
	}
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"configuration_id": configuration.ID, "run_id": runID, "point_set_hash": pointSetHash, "settings": input.Settings})
	model := saasstore.SurrogateModelSnapshot{OwnerUserID: userID, ConfigurationID: configuration.ID, RunID: runID, SnapshotKey: "p10-surrogate:" + compute.HashBytes(identityRaw), SchemaVersion: core.SurrogateVersion, TrainingPointSetHash: pointSetHash, BatchFoldHash: compute.HashBytes(identityRaw), ModelSettings: mustCanonical(input.Settings), OOFMetrics: saasstore.JSONB(`{}`), Artifact: saasstore.JSONB(`{}`), Status: task.Status, ComputeTaskID: &task.ID}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_key"}}, DoNothing: true}).Create(&model)
	if result.Error != nil {
		return SurrogateDescriptor{}, result.Error
	}
	if result.RowsAffected == 0 {
		if err := s.db.WithContext(ctx).Where("snapshot_key = ?", model.SnapshotKey).First(&model).Error; err != nil {
			return SurrogateDescriptor{}, err
		}
	}
	if task.Status != compute.TaskStatusCompleted {
		if _, err := s.computeTasks.StartTask(ctx, userID, task.ID); err != nil {
			return SurrogateDescriptor{}, err
		}
	}
	return s.GetSurrogate(ctx, userID, model.ID)
}

func (s *Service) surrogateInput(ctx context.Context, runID, configurationID uint, canonical ConfigurationCanonical, seed uint64) (SurrogateExecutionInput, string, error) {
	var origins []struct {
		PointID     uint
		StageID     uint
		StageKey    string
		Coordinates saasstore.JSONB
		Metrics     saasstore.JSONB
	}
	if err := s.db.WithContext(ctx).Table("research_point_origins AS o").Select("o.point_id,o.stage_id,s.stage_key,p.coordinates,p.metrics").Joins("JOIN research_stages s ON s.id = o.stage_id").Joins("JOIN research_evaluation_points p ON p.id = o.point_id").Where("o.run_id = ? AND o.origin_type = ? AND p.configuration_id = ? AND p.status = ?", runID, "sobol", configurationID, "completed").Order("o.stage_id,o.point_id").Scan(&origins).Error; err != nil {
		return SurrogateExecutionInput{}, "", err
	}
	examples := make([]core.SurrogateExample, 0, len(origins))
	hashes := make([]string, 0, len(origins))
	for _, row := range origins {
		var coordinates []int
		var metrics struct {
			LogFinalNAVRatio float64 `json:"log_final_nav_ratio"`
			LogDrawdown      float64 `json:"log_drawdown_residual_ratio"`
		}
		if json.Unmarshal(row.Coordinates, &coordinates) != nil || json.Unmarshal(row.Metrics, &metrics) != nil {
			return SurrogateExecutionInput{}, "", ErrInvalidRequest
		}
		examples = append(examples, core.SurrogateExample{Coordinates: coordinates, Batch: row.StageKey, LogFinalNAVRatio: metrics.LogFinalNAVRatio, LogDrawdownRatio: metrics.LogDrawdown})
		hashes = append(hashes, fmt.Sprintf("%d", row.PointID))
	}
	sort.Strings(hashes)
	hashRaw, _ := compute.CanonicalJSON(hashes)
	pointSetHash := compute.HashBytes(hashRaw)
	n0 := core.InitialSobolCount(len(canonical.ParameterSpace.Axes))
	settings := core.DefaultForestSettings(seed)
	return SurrogateExecutionInput{SchemaVersion: ConfigurationSchemaVersion, N0: n0, Settings: settings, Examples: examples}, pointSetHash, nil
}

func (s *Service) GetSurrogate(ctx context.Context, userID, id uint) (SurrogateDescriptor, error) {
	var model saasstore.SurrogateModelSnapshot
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&model).Error; err != nil {
		return SurrogateDescriptor{}, ErrNotFound
	}
	if model.ComputeTaskID != nil && model.Status != "completed" {
		task, err := s.computeTasks.Get(ctx, userID, *model.ComputeTaskID)
		if err == nil {
			if task.Status == compute.TaskStatusCompleted {
				var item saasstore.ComputeTaskItem
				if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).First(&item).Error; err != nil {
					return SurrogateDescriptor{}, err
				}
				var result SurrogateExecutionResult
				if err := json.Unmarshal(item.Result, &result); err != nil {
					return SurrogateDescriptor{}, err
				}
				artifactRaw, _ := compute.CanonicalJSON(result.Artifact)
				oofRaw, _ := compute.CanonicalJSON(map[string]any{"return": result.Artifact.ReturnOOF, "drawdown": result.Artifact.DrawdownOOF})
				updates := map[string]any{"status": "completed", "artifact": artifactRaw, "artifact_hash": result.ContentHash, "content_hash": result.ContentHash, "oof_metrics": oofRaw, "can_guide_return": result.Artifact.ReturnOOF.CanGuide, "can_guide_drawdown": result.Artifact.DrawdownOOF.CanGuide, "can_guide_conservative": result.Artifact.ReturnOOF.CanGuide && result.Artifact.DrawdownOOF.CanGuide}
				if err := s.db.WithContext(ctx).Model(&model).Updates(updates).Error; err != nil {
					return SurrogateDescriptor{}, err
				}
				model.Status = "completed"
				model.Artifact = artifactRaw
				model.ArtifactHash = result.ContentHash
				model.ContentHash = result.ContentHash
				model.CanGuideReturn = result.Artifact.ReturnOOF.CanGuide
				model.CanGuideDrawdown = result.Artifact.DrawdownOOF.CanGuide
				model.CanGuideConservative = model.CanGuideReturn && model.CanGuideDrawdown
			} else {
				_ = s.db.WithContext(ctx).Model(&model).Update("status", task.Status).Error
				model.Status = task.Status
			}
		}
	}
	descriptor := SurrogateDescriptor{ID: model.ID, ConfigurationID: model.ConfigurationID, RunID: model.RunID, Status: model.Status, ComputeTaskID: model.ComputeTaskID, TrainingPointSetHash: model.TrainingPointSetHash, CanGuideReturn: model.CanGuideReturn, CanGuideDrawdown: model.CanGuideDrawdown, CanGuideConservative: model.CanGuideConservative, ContentHash: model.ContentHash}
	if model.Status == "completed" {
		var artifact core.SurrogateArtifact
		if json.Unmarshal(model.Artifact, &artifact) == nil {
			descriptor.Artifact = &artifact
		}
	}
	return descriptor, nil
}

func (s *Service) ListSurrogates(ctx context.Context, userID, runID uint) ([]SurrogateDescriptor, error) {
	if _, _, _, err := s.loadRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	var rows []saasstore.SurrogateModelSnapshot
	if err := s.db.WithContext(ctx).Where("run_id = ? AND owner_user_id = ?", runID, userID).Order("created_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]SurrogateDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor, err := s.GetSurrogate(ctx, userID, row.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) CreateProposals(ctx context.Context, userID, surrogateID uint, req ProposalRequest) ([]ProposalDescriptor, error) {
	descriptor, err := s.GetSurrogate(ctx, userID, surrogateID)
	if err != nil || descriptor.Artifact == nil {
		return nil, ErrInvalidRequest
	}
	run, configuration, canonical, err := s.loadRun(ctx, userID, descriptor.RunID)
	if err != nil {
		return nil, err
	}
	existing, err := s.existingPointHashes(ctx, configuration.ID)
	if err != nil {
		return nil, err
	}
	poolPlan, err := core.PlanGlobal(canonical.ParameterSpace, canonical.BaseCoordinates, maxInt(req.Count*16, 64), run.NextSobolIndex, existing, false)
	if err != nil {
		return nil, err
	}
	coordinates := make([][]int, len(poolPlan.Points))
	pointByCoordinate := map[string]core.PlannedPoint{}
	for i, point := range poolPlan.Points {
		coordinates[i] = point.Coordinates
		pointByCoordinate[coordinateKey(point.Coordinates)] = point
	}
	scored := core.ScoreProposalPool(*descriptor.Artifact, coordinates)
	selected, err := core.SelectProposals(req.Kind, scored, req.Count, *descriptor.Artifact)
	if err != nil {
		return nil, err
	}
	poolRaw, _ := compute.CanonicalJSON(poolPlan)
	poolHash := compute.HashBytes(poolRaw)
	result := []ProposalDescriptor{}
	for _, prediction := range selected {
		planned := pointByCoordinate[coordinateKey(prediction.Coordinates)]
		typesRaw, _ := compute.CanonicalJSON([]string{req.Kind})
		coordinatesRaw, _ := compute.CanonicalJSON(planned.Coordinates)
		parametersRaw, _ := compute.CanonicalJSON(planned.Parameters)
		predictionRaw, _ := compute.CanonicalJSON(prediction)
		uncertaintyRaw, _ := compute.CanonicalJSON(map[string]any{"return_dispersion": prediction.ReturnDispersion, "drawdown_dispersion": prediction.DrawdownDispersion})
		model := saasstore.SurrogateProposal{SurrogateSnapshotID: surrogateID, VectorHash: planned.VectorHash, ProposalTypes: typesRaw, Coordinates: coordinatesRaw, Parameters: parametersRaw, Predictions: predictionRaw, Uncertainty: uncertaintyRaw, CandidatePoolHash: poolHash, ActualError: saasstore.JSONB(`{}`)}
		create := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "surrogate_snapshot_id"}, {Name: "vector_hash"}}, DoNothing: true}).Create(&model)
		if create.Error != nil {
			return nil, create.Error
		}
		if create.RowsAffected == 0 {
			if err := s.db.WithContext(ctx).Where("surrogate_snapshot_id = ? AND vector_hash = ?", surrogateID, planned.VectorHash).First(&model).Error; err != nil {
				return nil, err
			}
			var oldTypes []string
			_ = json.Unmarshal(model.ProposalTypes, &oldTypes)
			model.ProposalTypes = mustCanonical(cleanStrings(append(oldTypes, req.Kind)))
			_ = s.db.WithContext(ctx).Model(&model).Update("proposal_types", model.ProposalTypes).Error
		}
		result = append(result, ProposalDescriptor{ID: model.ID, Types: []string{req.Kind}, VectorHash: model.VectorHash, Coordinates: planned.Coordinates, Parameters: planned.Parameters, Prediction: prediction, ActualPointID: model.ActualPointID})
	}
	return result, nil
}

func (s *Service) ListProposals(ctx context.Context, userID, surrogateID uint) ([]ProposalDescriptor, error) {
	if _, err := s.GetSurrogate(ctx, userID, surrogateID); err != nil {
		return nil, err
	}
	var rows []saasstore.SurrogateProposal
	if err := s.db.WithContext(ctx).Where("surrogate_snapshot_id = ?", surrogateID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ProposalDescriptor, 0, len(rows))
	for _, row := range rows {
		var types []string
		var coordinates []int
		var parameters map[string]float64
		var prediction core.ProposalPrediction
		if json.Unmarshal(row.ProposalTypes, &types) != nil || json.Unmarshal(row.Coordinates, &coordinates) != nil || json.Unmarshal(row.Parameters, &parameters) != nil || json.Unmarshal(row.Predictions, &prediction) != nil {
			return nil, ErrInvalidRequest
		}
		result = append(result, ProposalDescriptor{ID: row.ID, Types: types, VectorHash: row.VectorHash, Coordinates: coordinates, Parameters: parameters, Prediction: prediction, ActualPointID: row.ActualPointID})
	}
	return result, nil
}

func (s *Service) CreateSeries(ctx context.Context, userID uint, req CreateSeriesRequest) (SeriesDescriptor, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.ConfigurationIDs) < 2 {
		return SeriesDescriptor{}, ErrInvalidRequest
	}
	configurations := make([]saasstore.ResearchConfiguration, 0, len(req.ConfigurationIDs))
	canonicals := make([]ConfigurationCanonical, 0, len(req.ConfigurationIDs))
	contexts := make([]core.ComparisonContext, 0, len(req.ConfigurationIDs))
	memberHashes := []string{}
	for _, id := range req.ConfigurationIDs {
		configuration, canonical, err := s.loadConfiguration(ctx, userID, id)
		if err != nil {
			return SeriesDescriptor{}, err
		}
		configurations = append(configurations, configuration)
		canonicals = append(canonicals, canonical)
		pointSetHash, err := s.configurationPointSetHash(ctx, id)
		if err != nil {
			return SeriesDescriptor{}, err
		}
		contexts = append(contexts, comparisonContext(configuration, canonical, pointSetHash))
		memberHashes = append(memberHashes, configuration.ConfigHash)
	}
	eligibility := core.CompareEligibility(contexts)
	backgroundRaw, _ := compute.CanonicalJSON(contexts)
	changedRaw, _ := compute.CanonicalJSON(cleanStrings(req.ChangedFactors))
	schemaRaw, _ := compute.CanonicalJSON(canonicals[0].ParameterSpace)
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"members": memberHashes, "changed": cleanStrings(req.ChangedFactors), "version": core.SeriesVersion})
	series := saasstore.ResearchSeries{OwnerUserID: userID, SeriesKey: "p10-series:" + compute.HashBytes(identityRaw), Name: req.Name, SchemaVersion: core.SeriesVersion, CommonBackgroundHash: compute.HashBytes(backgroundRaw), CommonBackground: backgroundRaw, ChangedFactors: changedRaw, CommonSchemaHash: compute.HashBytes(schemaRaw), CommonSchema: schemaRaw}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "series_key"}}, DoNothing: true}).Create(&series)
	if result.Error != nil {
		return SeriesDescriptor{}, result.Error
	}
	if result.RowsAffected == 0 {
		if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND series_key = ?", userID, series.SeriesKey).First(&series).Error; err != nil {
			return SeriesDescriptor{}, err
		}
	} else {
		for index, configuration := range configurations {
			factor := saasstore.JSONB(`{}`)
			if index < len(req.FactorValues) && len(req.FactorValues[index]) > 0 {
				factor, _ = compute.CanonicalRawJSON(req.FactorValues[index])
			}
			member := saasstore.ResearchSeriesMember{SeriesID: series.ID, ConfigurationID: configuration.ID, DisplayOrder: index, FactorValues: factor}
			if err := s.db.WithContext(ctx).Create(&member).Error; err != nil {
				return SeriesDescriptor{}, err
			}
		}
	}
	commonManifest, missing, differences := s.seriesComparisonPayload(ctx, configurations, eligibility.Level)
	manifestRaw, _ := compute.CanonicalJSON(commonManifest)
	missingRaw, _ := compute.CanonicalJSON(missing)
	differenceRaw, _ := compute.CanonicalJSON(differences)
	reasonsRaw, _ := compute.CanonicalJSON(eligibility.Reasons)
	membersRaw, _ := compute.CanonicalJSON(memberHashes)
	snapshotIdentity, _ := compute.CanonicalJSON(map[string]any{"series_id": series.ID, "members": memberHashes, "eligibility": eligibility, "manifest_hash": compute.HashBytes(manifestRaw), "version": core.ComparisonVersion})
	snapshot := saasstore.ResearchComparisonSnapshot{SeriesID: series.ID, SnapshotKey: "p10-comparison:" + compute.HashBytes(snapshotIdentity), SchemaVersion: core.ComparisonVersion, Eligibility: eligibility.Level, EligibilityReasons: reasonsRaw, MemberHashes: membersRaw, CommonManifestHash: compute.HashBytes(manifestRaw), CommonManifest: manifestRaw, Missing: missingRaw, Differences: differenceRaw, ContentHash: compute.HashBytes(snapshotIdentity)}
	create := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_key"}}, DoNothing: true}).Create(&snapshot)
	if create.Error != nil {
		return SeriesDescriptor{}, create.Error
	}
	if create.RowsAffected == 0 {
		_ = s.db.WithContext(ctx).Where("snapshot_key = ?", snapshot.SnapshotKey).First(&snapshot).Error
	}
	return SeriesDescriptor{ID: series.ID, Name: series.Name, ConfigurationIDs: req.ConfigurationIDs, Eligibility: eligibility, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash}, nil
}

func (s *Service) GetSeries(ctx context.Context, userID, id uint) (SeriesDescriptor, error) {
	var series saasstore.ResearchSeries
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&series).Error; err != nil {
		return SeriesDescriptor{}, ErrNotFound
	}
	var members []saasstore.ResearchSeriesMember
	if err := s.db.WithContext(ctx).Where("series_id = ?", series.ID).Order("display_order ASC").Find(&members).Error; err != nil {
		return SeriesDescriptor{}, err
	}
	ids := make([]uint, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ConfigurationID)
	}
	var snapshot saasstore.ResearchComparisonSnapshot
	if err := s.db.WithContext(ctx).Where("series_id = ?", series.ID).Order("created_at DESC,id DESC").First(&snapshot).Error; err != nil {
		return SeriesDescriptor{}, err
	}
	var reasons []string
	_ = json.Unmarshal(snapshot.EligibilityReasons, &reasons)
	return SeriesDescriptor{ID: series.ID, Name: series.Name, ConfigurationIDs: ids, Eligibility: core.ComparisonEligibility{Level: snapshot.Eligibility, Reasons: reasons}, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash}, nil
}

func (s *Service) configurationPointSetHash(ctx context.Context, id uint) (string, error) {
	var hashes []string
	if err := s.db.WithContext(ctx).Model(&saasstore.ResearchEvaluationPoint{}).Where("configuration_id = ? AND status = ?", id, "completed").Order("vector_hash ASC").Pluck("vector_hash", &hashes).Error; err != nil {
		return "", err
	}
	raw, _ := compute.CanonicalJSON(hashes)
	return compute.HashBytes(raw), nil
}
func comparisonContext(configuration saasstore.ResearchConfiguration, canonical ConfigurationCanonical, pointSetHash string) core.ComparisonContext {
	capitalRaw, _ := compute.CanonicalJSON(canonical.Backtest.InitialCapital)
	cashRaw, _ := compute.CanonicalJSON(canonical.Backtest.MonthlyDCA)
	costRaw, _ := compute.CanonicalJSON(map[string]any{"fee": canonical.Backtest.FeeRate, "spread": canonical.Backtest.SpreadRate})
	return core.ComparisonContext{InstrumentID: configuration.InstrumentID, DatasetHash: configuration.DatasetHash, Interval: configuration.Interval, StartTimeMs: configuration.StartTimeMs, EndTimeMs: configuration.EndTimeMs, StrategyVersion: sigmoidStrategyVersion(), BacktestCoreVersion: backtestCoreVersion(), ExecutionMode: configuration.ExecutionMode, InitialCapitalHash: compute.HashBytes(capitalRaw), CashFlowHash: compute.HashBytes(cashRaw), CostHash: compute.HashBytes(costRaw), BenchmarkVersion: "dca-v1", MetricVersion: "p08-relative-metrics-v1", ParameterSchemaHash: configuration.ParameterSpaceHash, ResultSchemaVersion: "standard-backtest-result-v1", PointSetHash: pointSetHash}
}
func (s *Service) seriesComparisonPayload(ctx context.Context, configurations []saasstore.ResearchConfiguration, level string) ([]string, map[uint][]string, []map[string]any) {
	sets := make([]map[string]saasstore.ResearchEvaluationPoint, len(configurations))
	all := map[string]bool{}
	for i, configuration := range configurations {
		var points []saasstore.ResearchEvaluationPoint
		_ = s.db.WithContext(ctx).Where("configuration_id = ? AND status = ?", configuration.ID, "completed").Find(&points).Error
		sets[i] = map[string]saasstore.ResearchEvaluationPoint{}
		for _, point := range points {
			sets[i][point.VectorHash] = point
			all[point.VectorHash] = true
		}
	}
	manifest := make([]string, 0, len(all))
	for hash := range all {
		manifest = append(manifest, hash)
	}
	sort.Strings(manifest)
	missing := map[uint][]string{}
	differences := []map[string]any{}
	for _, hash := range manifest {
		complete := true
		for i, configuration := range configurations {
			if _, ok := sets[i][hash]; !ok {
				missing[configuration.ID] = append(missing[configuration.ID], hash)
				complete = false
			}
		}
		if level == "paired_direct" && complete {
			base := sets[0][hash]
			var baseMetrics map[string]float64
			_ = json.Unmarshal(base.Metrics, &baseMetrics)
			for i := 1; i < len(sets); i++ {
				var metrics map[string]float64
				_ = json.Unmarshal(sets[i][hash].Metrics, &metrics)
				differences = append(differences, map[string]any{"vector_hash": hash, "left_configuration_id": configurations[0].ID, "right_configuration_id": configurations[i].ID, "log_final_nav_ratio_difference": metrics["log_final_nav_ratio"] - baseMetrics["log_final_nav_ratio"], "log_drawdown_residual_ratio_difference": metrics["log_drawdown_residual_ratio"] - baseMetrics["log_drawdown_residual_ratio"], "qualification_changed": sets[i][hash].Qualified != base.Qualified})
			}
		}
	}
	return manifest, missing, differences
}
func mustCanonical(value any) saasstore.JSONB { raw, _ := compute.CanonicalJSON(value); return raw }
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func sigmoidStrategyVersion() string { return "sigmoid-dca-v1" }
func backtestCoreVersion() string    { return "shared-backtest-core-v1" }

var _ = errors.Is
var _ = time.Now
var _ = gorm.ErrRecordNotFound
