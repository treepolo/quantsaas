package parameterresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	dynamiccore "quantsaas/internal/dynamicparam"
	core "quantsaas/internal/parameterresearch"
	"quantsaas/internal/quant"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	geometrysvc "quantsaas/internal/saas/geometry"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db           *gorm.DB
	computeTasks *computetask.Service
	robustness   *robustnesssvc.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service, robustness *robustnesssvc.Service) *Service {
	if robustness == nil {
		robustness = robustnesssvc.NewService(db, tasks)
	}
	return &Service{db: db, computeTasks: tasks, robustness: robustness}
}

func (s *Service) CreateConfiguration(ctx context.Context, userID uint, req CreateConfigurationRequest) (ConfigurationDescriptor, error) {
	if userID == 0 {
		return ConfigurationDescriptor{}, ErrInvalidRequest
	}
	canonical, dynamicReference, geometryReference, err := s.prepareConfiguration(ctx, userID, req)
	if err != nil {
		return ConfigurationDescriptor{}, err
	}
	canonicalRaw, err := compute.CanonicalJSON(canonical)
	if err != nil {
		return ConfigurationDescriptor{}, err
	}
	configHash := compute.HashBytes(canonicalRaw)
	spaceRaw, _ := compute.CanonicalJSON(canonical.ParameterSpace)
	tagsRaw, _ := compute.CanonicalJSON(cleanStrings(req.Tags))
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "參數績效地形研究"
	}
	model := saasstore.ResearchConfiguration{
		OwnerUserID: userID, ConfigHash: configHash, SchemaVersion: ConfigurationSchemaVersion,
		StrategyID: sigmoiddca.StrategyID, InstrumentID: canonical.Backtest.InstrumentID, DataSource: canonical.Backtest.DataSource,
		Symbol: canonical.Backtest.Symbol, Interval: canonical.Backtest.Interval, DatasetHash: canonical.DatasetHash,
		StartTimeMs: canonical.Backtest.StartTimeMs, EndTimeMs: canonical.Backtest.EndTimeMs, ExecutionMode: canonical.Backtest.ExecutionMode,
		ParameterSpaceVersion: canonical.ParameterSpace.SchemaVersion, ParameterSpaceHash: compute.HashBytes(spaceRaw), ParameterSpace: spaceRaw,
		DynamicMode: dynamicReference != nil, Canonical: canonicalRaw,
	}
	if dynamicReference != nil {
		model.DynamicStudyID, model.DynamicPolicyID = &dynamicReference.StudyID, &dynamicReference.PolicyArtifactID
	}
	_ = geometryReference
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "config_hash"}}, DoNothing: true}).Create(&model)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("owner_user_id = ? AND config_hash = ?", userID, configHash).First(&model).Error; err != nil {
				return err
			}
			return nil
		}
		metadata := saasstore.ResearchConfigurationMetadata{ConfigurationID: model.ID, Name: name, Notes: strings.TrimSpace(req.Notes), Tags: tagsRaw}
		return tx.Create(&metadata).Error
	})
	if err != nil {
		return ConfigurationDescriptor{}, err
	}
	return s.GetConfiguration(ctx, userID, model.ID)
}

func (s *Service) prepareConfiguration(ctx context.Context, userID uint, req CreateConfigurationRequest) (ConfigurationCanonical, *DynamicPackageReference, *GeometryPackageReference, error) {
	if req.GenomeID == 0 || robust.ValidateSpace(req.ParameterSpace) != nil || len(req.BaseCoordinates) != len(req.ParameterSpace.Axes) {
		return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
	}
	req.Backtest.InstrumentID = strings.TrimSpace(req.Backtest.InstrumentID)
	req.Backtest.DataSource = strings.TrimSpace(req.Backtest.DataSource)
	req.Backtest.Symbol = strings.ToUpper(strings.TrimSpace(req.Backtest.Symbol))
	req.Backtest.Interval = strings.TrimSpace(req.Backtest.Interval)
	req.Backtest.ExecutionMode = strings.TrimSpace(req.Backtest.ExecutionMode)
	if req.Backtest.InstrumentID == "" || req.Backtest.DataSource == "" || req.Backtest.Symbol == "" || req.Backtest.Interval == "" || req.Backtest.StartTimeMs <= 0 || req.Backtest.EndTimeMs <= req.Backtest.StartTimeMs {
		return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
	}
	if req.Backtest.ExecutionMode == "" {
		req.Backtest.ExecutionMode = saasstore.ExecutionModeCloseSameBar
	}
	if req.Backtest.ExecutionMode != saasstore.ExecutionModeCloseSameBar && req.Backtest.ExecutionMode != saasstore.ExecutionModeCloseNextOpen {
		return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", req.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
	}
	for i, value := range req.BaseCoordinates {
		axis := req.ParameterSpace.Axes[i]
		if value < axis.StudyStart || value > axis.StudyEnd {
			return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
		}
	}
	basePoint, err := pointForCoordinates(req.ParameterSpace, req.BaseCoordinates)
	if err != nil {
		return ConfigurationCanonical{}, nil, nil, err
	}
	baseParams := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	var dynamicReference *DynamicPackageReference
	if req.Dynamic == nil {
		chromosome, err := robust.ChromosomeWithValues(baseParams.Chromosome, basePoint.Parameters)
		if err != nil {
			return ConfigurationCanonical{}, nil, nil, err
		}
		if err := quant.ValidateChromosome(chromosome); err != nil {
			return ConfigurationCanonical{}, nil, nil, err
		}
	} else {
		dynamicReference, err = s.loadDynamicReference(ctx, userID, *req.Dynamic, req.ParameterSpace)
		if err != nil {
			return ConfigurationCanonical{}, nil, nil, err
		}
	}
	datasetHash, err := s.datasetHash(ctx, req.Backtest)
	if err != nil {
		return ConfigurationCanonical{}, nil, nil, err
	}
	var geometryReference *GeometryPackageReference
	if req.Dynamic != nil && req.Geometry != nil {
		return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
	}
	if req.Geometry != nil {
		var artifact saasstore.GeometryModelArtifact
		if err := s.db.WithContext(ctx).Where("id = ? AND horizon = ? AND dataset_hash = ? AND content_hash = ?", req.Geometry.ArtifactID, req.Geometry.Horizon, datasetHash, req.Geometry.ContentHash).First(&artifact).Error; err != nil {
			return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
		}
		var study saasstore.GeometryModelStudy
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND status = ? AND dataset_hash = ?", req.Geometry.StudyID, userID, geometrysvc.StudyStatusCompleted, datasetHash).First(&study).Error; err != nil || artifact.StudyID != study.ID {
			return ConfigurationCanonical{}, nil, nil, ErrInvalidRequest
		}
		geometryReference = &GeometryPackageReference{StudyID: study.ID, ArtifactID: artifact.ID, Horizon: artifact.Horizon, DatasetHash: artifact.DatasetHash, ContentHash: artifact.ContentHash, SchemaVersion: artifact.SchemaVersion}
	}
	return ConfigurationCanonical{SchemaVersion: ConfigurationSchemaVersion, GenomeID: req.GenomeID, ParameterSpace: req.ParameterSpace, BaseCoordinates: append([]int(nil), req.BaseCoordinates...), Backtest: req.Backtest, DatasetHash: datasetHash, DynamicPackage: dynamicReference, GeometryPackage: geometryReference}, dynamicReference, geometryReference, nil
}

func (s *Service) loadDynamicReference(ctx context.Context, userID uint, reference DynamicReference, space robust.ParameterSpace) (*DynamicPackageReference, error) {
	if reference.StudyID == 0 || reference.PolicyArtifactID == 0 {
		return nil, ErrInvalidRequest
	}
	var study saasstore.DynamicModelStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND artifact_set_hash <> ''", reference.StudyID, userID).First(&study).Error; err != nil {
		return nil, ErrInvalidRequest
	}
	var policy saasstore.DynamicPolicyArtifact
	if err := s.db.WithContext(ctx).Where("id = ? AND study_id = ? AND owner_user_id = ?", reference.PolicyArtifactID, study.ID, userID).First(&policy).Error; err != nil {
		return nil, ErrInvalidRequest
	}
	var prediction saasstore.DynamicPredictionSnapshot
	if err := s.db.WithContext(ctx).First(&prediction, policy.PredictionSnapshotID).Error; err != nil {
		return nil, err
	}
	var schema dynamiccore.DynamicParameterSpaceSchema
	if err := json.Unmarshal(policy.ParameterSpace, &schema); err != nil {
		return nil, err
	}
	variables := map[string]dynamiccore.ParameterVariable{}
	for _, variable := range schema.Variables {
		variables[variable.StableID] = variable
	}
	for _, axis := range space.Axes {
		variable, ok := variables[axis.Name]
		if !ok || axis.LegalMin < variable.Lower-1e-9 || axis.LegalMax > variable.Upper+1e-9 || axis.Step+1e-12 < variable.MinimumStep {
			return nil, ErrInvalidRequest
		}
	}
	return &DynamicPackageReference{StudyID: study.ID, PolicyArtifactID: policy.ID, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotID: prediction.ID, PredictionSnapshotHash: prediction.ContentHash, BasePolicyHash: policy.ContentHash, ParameterSpaceVersion: policy.ParameterSpaceVersion, ParameterSpaceHash: policy.ParameterSpaceHash}, nil
}

func (s *Service) DynamicSpace(ctx context.Context, userID, policyID uint) (DynamicSpaceDescriptor, error) {
	var policy saasstore.DynamicPolicyArtifact
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", policyID, userID).First(&policy).Error; err != nil {
		return DynamicSpaceDescriptor{}, ErrNotFound
	}
	var schema dynamiccore.DynamicParameterSpaceSchema
	var bundle dynamicparamsvc.PolicyBundle
	if err := json.Unmarshal(policy.ParameterSpace, &schema); err != nil {
		return DynamicSpaceDescriptor{}, err
	}
	if err := json.Unmarshal(policy.Payload, &bundle); err != nil {
		return DynamicSpaceDescriptor{}, err
	}
	values := map[string]float64{}
	for _, variable := range schema.Variables {
		for _, control := range bundle.Policy.Controls {
			if control.ParameterID != variable.ParameterID || control.Mode != variable.ControlMode {
				continue
			}
			switch variable.Role {
			case "global":
				values[variable.StableID] = control.GlobalValue
			case "base_logit":
				values[variable.StableID] = control.BaseLogit
			case "linear", "quadratic":
				for _, term := range control.Terms {
					if term.Input == variable.PredictionInput {
						if variable.Role == "linear" {
							values[variable.StableID] = term.Linear
						} else {
							values[variable.StableID] = term.Quadratic
						}
					}
				}
			case "direction", "volatility", "interaction":
				var effects map[string]float64
				if variable.Role == "direction" {
					effects = control.Effects.Direction
				} else if variable.Role == "volatility" {
					effects = control.Effects.Volatility
				} else {
					effects = control.Effects.Interaction
				}
				keys := make([]string, 0, len(effects))
				for key := range effects {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				if len(keys) > 0 {
					values[variable.StableID] = effects[keys[0]]
				}
			}
		}
	}
	return DynamicSpaceDescriptor{StudyID: policy.StudyID, PolicyArtifactID: policy.ID, Schema: schema, BaseValues: values}, nil
}

func (s *Service) datasetHash(ctx context.Context, settings robustnesssvc.BacktestSettings) (string, error) {
	if strings.TrimSpace(settings.MarketDataContentHash) != "" {
		return strings.TrimSpace(settings.MarketDataContentHash), nil
	}
	var rows []saasstore.KLine
	query := s.db.WithContext(ctx).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", settings.InstrumentID, settings.DataSource, settings.Symbol, settings.Interval, settings.StartTimeMs, settings.EndTimeMs).Order("open_time ASC").Find(&rows)
	if query.Error != nil {
		return "", query.Error
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("研究區間沒有行情資料")
	}
	bars := make([]quant.Bar, len(rows))
	for i, row := range rows {
		bars[i] = quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume}
	}
	return backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
}

func (s *Service) ListConfigurations(ctx context.Context, userID uint, limit int) ([]ConfigurationDescriptor, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var rows []saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", userID).Order("created_at DESC,id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ConfigurationDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor, err := s.describeConfiguration(ctx, row)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) GetConfiguration(ctx context.Context, userID, id uint) (ConfigurationDescriptor, error) {
	var row saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&row).Error; err != nil {
		return ConfigurationDescriptor{}, ErrNotFound
	}
	return s.describeConfiguration(ctx, row)
}

func (s *Service) describeConfiguration(ctx context.Context, row saasstore.ResearchConfiguration) (ConfigurationDescriptor, error) {
	var canonical ConfigurationCanonical
	if err := json.Unmarshal(row.Canonical, &canonical); err != nil {
		return ConfigurationDescriptor{}, err
	}
	var metadata saasstore.ResearchConfigurationMetadata
	if err := s.db.WithContext(ctx).Where("configuration_id = ?", row.ID).First(&metadata).Error; err != nil {
		return ConfigurationDescriptor{}, err
	}
	var tags []string
	_ = json.Unmarshal(metadata.Tags, &tags)
	return ConfigurationDescriptor{ID: row.ID, Name: metadata.Name, Notes: metadata.Notes, Tags: tags, ConfigHash: row.ConfigHash, SchemaVersion: row.SchemaVersion, InstrumentID: row.InstrumentID, DataSource: row.DataSource, Symbol: row.Symbol, Interval: row.Interval, DatasetHash: row.DatasetHash, StartTimeMs: row.StartTimeMs, EndTimeMs: row.EndTimeMs, ExecutionMode: row.ExecutionMode, ParameterSpaceVersion: row.ParameterSpaceVersion, ParameterSpaceHash: row.ParameterSpaceHash, ParameterSpace: canonical.ParameterSpace, BaseCoordinates: canonical.BaseCoordinates, DynamicMode: row.DynamicMode, DynamicPackage: canonical.DynamicPackage, GeometryPackage: canonical.GeometryPackage, Archived: row.ArchivedAt != nil, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)}, nil
}

func (s *Service) ArchiveConfiguration(ctx context.Context, userID, id uint) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&saasstore.ResearchConfiguration{}).Where("id = ? AND owner_user_id = ?", id, userID).Update("archived_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return s.db.WithContext(ctx).Model(&saasstore.ResearchConfigurationMetadata{}).Where("configuration_id = ?", id).Update("archived_at", now).Error
}

func pointForCoordinates(space robust.ParameterSpace, coordinate []int) (core.PlannedPoint, error) {
	if len(coordinate) != len(space.Axes) {
		return core.PlannedPoint{}, ErrInvalidRequest
	}
	parameters := map[string]float64{}
	for name, value := range space.Fixed {
		parameters[name] = value
	}
	for i, value := range coordinate {
		axis := space.Axes[i]
		if value < axis.StudyStart || value > axis.StudyEnd || value < 0 || value >= len(axis.Values) {
			return core.PlannedPoint{}, ErrInvalidRequest
		}
		parameters[axis.Name] = axis.Values[value]
	}
	raw, _ := compute.CanonicalJSON(coordinate)
	return core.PlannedPoint{Coordinates: append([]int(nil), coordinate...), Parameters: parameters, VectorHash: compute.HashBytes(raw)}, nil
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func boolPtr(value bool) *bool { return &value }
func floatMapClone(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func parseCoordinateKey(value string) ([]int, error) {
	parts := strings.Split(value, ":")
	result := make([]int, len(parts))
	for i, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		result[i] = parsed
	}
	return result, nil
}
