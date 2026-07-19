package perturbation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	core "quantsaas/internal/perturbation"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const candidateAnalysisVersion = "p13-analysis-link-v1"

func (s *Service) PlanTest(ctx context.Context, userID uint, request TestPlanRequest) (TestPlan, error) {
	_, snapshot, err := s.loadGroupSnapshot(ctx, userID, request.GroupID)
	if err != nil {
		return TestPlan{}, err
	}
	if request.Backtest.StrategyID == "" {
		request.Backtest.StrategyID = sigmoiddca.StrategyID
	}
	if request.Backtest.StrategyID != sigmoiddca.StrategyID {
		return TestPlan{}, fmt.Errorf("%w: 受測參數不屬於目前支援的策略", ErrIncompatibleSubject)
	}
	if request.Backtest.ExecutionMode == "" {
		return TestPlan{}, fmt.Errorf("%w: 請選擇執行假設", ErrIncompatibleSubject)
	}
	if request.Backtest.StartTimeMs < snapshot.StartTimeMs || request.Backtest.EndTimeMs > snapshot.EndTimeMs || request.Backtest.EndTimeMs < request.Backtest.StartTimeMs {
		return TestPlan{}, fmt.Errorf("%w: 測試日期必須位於資料群組範圍內", ErrIncompatibleSubject)
	}
	if len(request.Subjects) == 0 {
		return TestPlan{}, fmt.Errorf("%w: 請至少選擇一個受測項目", ErrIncompatibleSubject)
	}
	subjects := make([]SubjectDescriptor, 0, len(request.Subjects))
	seen := map[string]bool{}
	for index, subjectReq := range request.Subjects {
		subject, resolveErr := s.resolveSubject(ctx, userID, index, subjectReq, request.Backtest, snapshot)
		if resolveErr != nil {
			return TestPlan{}, resolveErr
		}
		if seen[subject.SubjectHash] {
			continue
		}
		seen[subject.SubjectHash] = true
		subjects = append(subjects, subject)
	}
	if len(subjects) == 0 {
		return TestPlan{}, ErrIncompatibleSubject
	}
	var variantCount int64
	if err := s.db.WithContext(ctx).Model(&saasstore.PerturbationVariant{}).Where("group_id=? AND owner_user_id=? AND status=? AND integrity_status=? AND archived_at IS NULL", request.GroupID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).Count(&variantCount).Error; err != nil {
		return TestPlan{}, err
	}
	identity := map[string]any{"schema_version": TestSchemaVersion, "group_id": request.GroupID, "snapshot_hash": snapshot.SourceContentHash, "backtest": request.Backtest, "subjects": subjects}
	specHash, _, err := core.CanonicalHash(identity)
	if err != nil {
		return TestPlan{}, err
	}
	planHash, _, err := core.CanonicalHash(map[string]any{"test_spec_hash": specHash, "name": strings.TrimSpace(request.Name), "notes": strings.TrimSpace(request.Notes), "tags": cleanStrings(request.Tags)})
	if err != nil {
		return TestPlan{}, err
	}
	planned := len(subjects) * (1 + int(variantCount))
	return TestPlan{SchemaVersion: TestSchemaVersion, PlanHash: "perturbation-test-plan:v1:" + planHash, TestSpecHash: "perturbation-test-spec:v1:" + specHash, GroupID: request.GroupID, SubjectCount: len(subjects), VariantCount: int(variantCount), PlannedRuns: planned, PendingRuns: planned, Subjects: subjects}, nil
}

func (s *Service) resolveSubject(ctx context.Context, userID uint, ordinal int, request SubjectRequest, settings BacktestSettings, snapshot saasstore.PerturbationSourceSnapshot) (SubjectDescriptor, error) {
	kind := strings.ToLower(strings.TrimSpace(request.SourceKind))
	switch kind {
	case "robust_candidate", "m_candidate":
		if s.parameterResearch == nil {
			return SubjectDescriptor{}, computetask.ErrServiceUnavailable
		}
		var candidate saasstore.RobustCandidate
		var point saasstore.ResearchEvaluationPoint
		if s.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND archived_at IS NULL", request.SourceID, userID).First(&candidate).Error != nil || s.db.WithContext(ctx).First(&point, candidate.PointID).Error != nil {
			return SubjectDescriptor{}, ErrNotFound
		}
		var parameters map[string]float64
		if json.Unmarshal(point.Parameters, &parameters) != nil {
			return SubjectDescriptor{}, ErrIncompatibleSubject
		}
		execution, err := s.parameterResearch.BuildPointExecutionInput(ctx, userID, candidate.ConfigurationID, parameters)
		if err != nil {
			return SubjectDescriptor{}, err
		}
		if execution.Backtest.Interval != snapshot.Interval || execution.Backtest.ExecutionMode != settings.ExecutionMode {
			return SubjectDescriptor{}, fmt.Errorf("%w: 候選 #%d 的週期或執行假設與資料群組不一致", ErrIncompatibleSubject, request.SourceID)
		}
		candidateID := candidate.ID
		return SubjectDescriptor{Ordinal: ordinal, SourceKind: "robust_candidate", SourceID: candidate.ID, SourceVersion: candidate.Version, SubjectHash: candidate.AdoptionUnitHash, AdoptionUnit: append(json.RawMessage(nil), candidate.AdoptionUnit...), Dynamic: execution.Dynamic, CandidateID: &candidateID}, nil
	case "gene_record", "gene":
		var gene saasstore.GeneRecord
		if s.db.WithContext(ctx).Where("id=? AND strategy_id=?", request.SourceID, sigmoiddca.StrategyID).First(&gene).Error != nil {
			return SubjectDescriptor{}, ErrNotFound
		}
		if gene.Interval != snapshot.Interval || gene.ExecutionMode != settings.ExecutionMode {
			return SubjectDescriptor{}, fmt.Errorf("%w: 參數 #%d 使用 %s／%s，但資料群組測試設定為 %s／%s", ErrIncompatibleSubject, gene.ID, gene.Interval, gene.ExecutionMode, snapshot.Interval, settings.ExecutionMode)
		}
		params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
		adoption, err := compute.CanonicalJSON(params)
		if err != nil {
			return SubjectDescriptor{}, err
		}
		hash := compute.HashBytes(adoption)
		return SubjectDescriptor{Ordinal: ordinal, SourceKind: "gene_record", SourceID: gene.ID, SourceVersion: "gene-record-v1", SubjectHash: hash, AdoptionUnit: adoption}, nil
	case "backtest_result":
		var count int64
		if s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("user_id=? AND backtest_result_id=?", userID, request.SourceID).Count(&count).Error != nil || count == 0 {
			return SubjectDescriptor{}, ErrNotFound
		}
		var result saasstore.BacktestResult
		var spec saasstore.BacktestSpec
		if s.db.WithContext(ctx).Where("id=? AND status=?", request.SourceID, saasstore.BacktestResultStatusCompleted).First(&result).Error != nil || s.db.WithContext(ctx).First(&spec, result.BacktestSpecID).Error != nil {
			return SubjectDescriptor{}, ErrNotFound
		}
		identity, err := backtestresult.DecodeIdentity(spec.Snapshot)
		if err != nil {
			return SubjectDescriptor{}, err
		}
		if identity.Snapshot.Interval != snapshot.Interval || identity.Snapshot.ExecutionMode != settings.ExecutionMode {
			return SubjectDescriptor{}, fmt.Errorf("%w: 回測結果 #%d 的週期或執行假設與資料群組不一致", ErrIncompatibleSubject, request.SourceID)
		}
		adoption := append(json.RawMessage(nil), identity.Snapshot.Parameters...)
		dynamic := identity.Snapshot.ModelArtifactHash != ""
		execution := parameterresearchsvc.PointExecutionInput{ExecutorType: "p08.robustness.point", Backtest: requestForSettings(settings, snapshot, adoption)}
		if dynamic {
			var material saasstore.DynamicMaterialization
			var study saasstore.DynamicModelStudy
			var policy saasstore.DynamicPolicyArtifact
			var prediction saasstore.DynamicPredictionSnapshot
			if s.db.WithContext(ctx).Where("backtest_result_id=?", result.ID).Order("id DESC").First(&material).Error != nil || s.db.WithContext(ctx).First(&study, material.StudyID).Error != nil || s.db.WithContext(ctx).First(&policy, material.PolicyArtifactID).Error != nil || s.db.WithContext(ctx).First(&prediction, material.PredictionSnapshotID).Error != nil {
				return SubjectDescriptor{}, ErrIncompatibleSubject
			}
			input := dynamicparamsvc.MaterializeExecutionInput{SchemaVersion: dynamicparamsvc.MaterializeInputVersion, StudyID: study.ID, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotHash: prediction.ContentHash, PolicyHash: policy.ContentHash, Scope: dynamicparamsvc.MarketScope{}, Backtest: execution.Backtest}
			inputRaw, _ := compute.CanonicalJSON(input)
			execution.ExecutorType = dynamicparamsvc.MaterializeExecutorType
			execution.Input = inputRaw
			execution.Dynamic = true
			adoption, _ = compute.CanonicalJSON(map[string]any{"schema_version": "p13-frozen-dynamic-adoption-v1", "artifact_set_hash": study.ArtifactSetHash, "prediction_snapshot_hash": prediction.ContentHash, "policy_hash": policy.ContentHash, "policy_payload": json.RawMessage(policy.Payload), "base_parameters": identity.Snapshot.Parameters, "backtest_result_hash": result.ContentHash})
		}
		hash := compute.HashBytes(adoption)
		return SubjectDescriptor{Ordinal: ordinal, SourceKind: "backtest_result", SourceID: result.ID, SourceVersion: result.ResultVersion, SubjectHash: hash, AdoptionUnit: adoption, Dynamic: dynamic}, nil
	default:
		return SubjectDescriptor{}, ErrIncompatibleSubject
	}
}

func requestForSettings(settings BacktestSettings, snapshot saasstore.PerturbationSourceSnapshot, params json.RawMessage) backtest.CreateRequest {
	return backtest.CreateRequest{StrategyID: settings.StrategyID, InstrumentID: snapshot.OriginalInstrumentID, DataSource: snapshot.OriginalDataSource, ExecutionMode: settings.ExecutionMode, StartTimeMs: settings.StartTimeMs, EndTimeMs: settings.EndTimeMs, Symbol: snapshot.OriginalSymbol, Pair: snapshot.OriginalSymbol, Interval: snapshot.Interval, Source: backtest.SourceCustom, CustomParams: params, InitialCapital: settings.InitialCapital, MonthlyDCA: settings.MonthlyDCA, FeeRate: settings.FeeRate, SpreadRate: settings.SpreadRate, LongTermFilterEnabled: settings.LongTermFilterEnabled, LongTermFilterMonths: settings.LongTermFilterMonths}
}

func (s *Service) CreateTest(ctx context.Context, userID uint, request StartTestRequest) (TestDescriptor, error) {
	if request.Backtest.StrategyID == "" {
		request.Backtest.StrategyID = sigmoiddca.StrategyID
	}
	plan, err := s.PlanTest(ctx, userID, TestPlanRequest{CreateTestRequest: request.CreateTestRequest})
	if err != nil {
		return TestDescriptor{}, err
	}
	if plan.PlanHash != strings.TrimSpace(request.PlanHash) {
		return TestDescriptor{}, ErrStalePlan
	}
	tags, _ := compute.CanonicalJSON(cleanStrings(request.Tags))
	settingsRaw, _ := compute.CanonicalJSON(request.Backtest)
	var test saasstore.PerturbationTest
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := saasstore.PerturbationTest{OwnerUserID: userID, GroupID: request.GroupID, TestSpecHash: plan.TestSpecHash, SchemaVersion: TestSchemaVersion, Name: firstNonempty(strings.TrimSpace(request.Name), "擾動行情測試"), Notes: strings.TrimSpace(request.Notes), Tags: tags, Status: "draft", BacktestSettings: settingsRaw}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "test_spec_hash"}}, DoNothing: true}).Create(&candidate)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("test_spec_hash=? AND owner_user_id=?", plan.TestSpecHash, userID).First(&test).Error; err != nil {
				return err
			}
			return nil
		}
		test = candidate
		return s.persistSubjects(ctx, tx, userID, test, request, plan.Subjects)
	})
	if err != nil {
		return TestDescriptor{}, err
	}
	return s.GetTest(ctx, userID, test.ID, true)
}

func (s *Service) persistSubjects(ctx context.Context, tx *gorm.DB, userID uint, test saasstore.PerturbationTest, request StartTestRequest, planned []SubjectDescriptor) error {
	_, snapshot, err := s.loadGroupSnapshot(ctx, userID, test.GroupID)
	if err != nil {
		return err
	}
	for index, subjectReq := range request.Subjects {
		descriptor, err := s.resolveSubject(ctx, userID, index, subjectReq, request.Backtest, snapshot)
		if err != nil {
			return err
		}
		var execution parameterresearchsvc.PointExecutionInput
		switch descriptor.SourceKind {
		case "robust_candidate":
			var candidate saasstore.RobustCandidate
			var point saasstore.ResearchEvaluationPoint
			if tx.First(&candidate, descriptor.SourceID).Error != nil || tx.First(&point, candidate.PointID).Error != nil {
				return ErrNotFound
			}
			var values map[string]float64
			_ = json.Unmarshal(point.Parameters, &values)
			execution, err = s.parameterResearch.BuildPointExecutionInput(ctx, userID, candidate.ConfigurationID, values)
		case "gene_record":
			var gene saasstore.GeneRecord
			if tx.First(&gene, descriptor.SourceID).Error != nil {
				return ErrNotFound
			}
			params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
			raw, _ := compute.CanonicalJSON(params)
			execution = parameterresearchsvc.PointExecutionInput{ExecutorType: "p08.robustness.point", Backtest: requestForSettings(request.Backtest, snapshot, raw)}
		case "backtest_result":
			execution, err = s.executionForBacktestSubject(ctx, userID, descriptor.SourceID, request.Backtest, snapshot)
		}
		if err != nil {
			return err
		}
		executionRaw, _ := compute.CanonicalJSON(execution)
		row := saasstore.PerturbationTestSubject{TestID: test.ID, Ordinal: index, SourceKind: descriptor.SourceKind, SourceID: descriptor.SourceID, SourceVersion: descriptor.SourceVersion, SubjectHash: descriptor.SubjectHash, AdoptionUnit: saasstore.JSONB(descriptor.AdoptionUnit), ExecutionInput: executionRaw, Dynamic: descriptor.Dynamic, CandidateID: descriptor.CandidateID}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "test_id"}, {Name: "subject_hash"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if descriptor.CandidateID != nil {
			partial := mustJSON(map[string]any{"test_id": test.ID, "subject_hash": descriptor.SubjectHash, "back_link": fmt.Sprintf("/generator?mode=perturbation&test=%d", test.ID)})
			link := saasstore.CandidateAnalysisLink{CandidateID: *descriptor.CandidateID, AnalysisKind: "L", Version: candidateAnalysisVersion, Status: "not_calculated", SourceID: strconv.FormatUint(uint64(test.ID), 10), SourceVersion: TestSchemaVersion, PartialSnapshot: partial}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "candidate_id"}, {Name: "analysis_kind"}, {Name: "version"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "source_id", "source_version", "partial_snapshot", "updated_at"})}).Create(&link).Error; err != nil {
				return err
			}
		}
	}
	_ = planned
	return nil
}

func (s *Service) executionForBacktestSubject(ctx context.Context, userID, resultID uint, settings BacktestSettings, snapshot saasstore.PerturbationSourceSnapshot) (parameterresearchsvc.PointExecutionInput, error) {
	var result saasstore.BacktestResult
	var spec saasstore.BacktestSpec
	if s.db.WithContext(ctx).First(&result, resultID).Error != nil || s.db.WithContext(ctx).First(&spec, result.BacktestSpecID).Error != nil {
		return parameterresearchsvc.PointExecutionInput{}, ErrNotFound
	}
	identity, err := backtestresult.DecodeIdentity(spec.Snapshot)
	if err != nil {
		return parameterresearchsvc.PointExecutionInput{}, err
	}
	execution := parameterresearchsvc.PointExecutionInput{ExecutorType: "p08.robustness.point", Backtest: requestForSettings(settings, snapshot, identity.Snapshot.Parameters)}
	if identity.Snapshot.ModelArtifactHash == "" {
		return execution, nil
	}
	var material saasstore.DynamicMaterialization
	var study saasstore.DynamicModelStudy
	var policy saasstore.DynamicPolicyArtifact
	var prediction saasstore.DynamicPredictionSnapshot
	if s.db.WithContext(ctx).Where("backtest_result_id=?", result.ID).Order("id DESC").First(&material).Error != nil || s.db.WithContext(ctx).First(&study, material.StudyID).Error != nil || s.db.WithContext(ctx).First(&policy, material.PolicyArtifactID).Error != nil || s.db.WithContext(ctx).First(&prediction, material.PredictionSnapshotID).Error != nil {
		return execution, ErrIncompatibleSubject
	}
	input := dynamicparamsvc.MaterializeExecutionInput{SchemaVersion: dynamicparamsvc.MaterializeInputVersion, StudyID: study.ID, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotHash: prediction.ContentHash, PolicyHash: policy.ContentHash, Backtest: execution.Backtest}
	raw, _ := compute.CanonicalJSON(input)
	execution.ExecutorType = dynamicparamsvc.MaterializeExecutorType
	execution.Input = raw
	execution.Dynamic = true
	return execution, nil
}

func (s *Service) ListTests(ctx context.Context, userID uint, includeArchived bool) ([]TestDescriptor, error) {
	var rows []saasstore.PerturbationTest
	q := s.db.WithContext(ctx).Where("owner_user_id=?", userID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}
	if err := q.Order("updated_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]TestDescriptor, 0, len(rows))
	for _, row := range rows {
		item, err := s.GetTest(ctx, userID, row.ID, false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
func (s *Service) GetTest(ctx context.Context, userID, testID uint, includeSubjects bool) (TestDescriptor, error) {
	if err := s.syncTest(ctx, userID, testID); err != nil && !errors.Is(err, computetask.ErrServiceUnavailable) {
		return TestDescriptor{}, err
	}
	var row saasstore.PerturbationTest
	if s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", testID, userID).First(&row).Error != nil {
		return TestDescriptor{}, ErrNotFound
	}
	tags := []string{}
	_ = json.Unmarshal(row.Tags, &tags)
	var settings BacktestSettings
	_ = json.Unmarshal(row.BacktestSettings, &settings)
	out := TestDescriptor{ID: row.ID, GroupID: row.GroupID, Name: row.Name, Notes: row.Notes, Tags: tags, Status: row.Status, TestSpecHash: row.TestSpecHash, Backtest: settings, LatestSnapshotID: row.LatestSnapshotID, Archived: row.ArchivedAt != nil, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if includeSubjects {
		var subjects []saasstore.PerturbationTestSubject
		if err := s.db.WithContext(ctx).Where("test_id=?", row.ID).Order("ordinal ASC").Find(&subjects).Error; err != nil {
			return TestDescriptor{}, err
		}
		for _, subject := range subjects {
			out.Subjects = append(out.Subjects, subjectDescriptor(subject))
		}
	}
	return out, nil
}
func subjectDescriptor(row saasstore.PerturbationTestSubject) SubjectDescriptor {
	return SubjectDescriptor{ID: row.ID, Ordinal: row.Ordinal, SourceKind: row.SourceKind, SourceID: row.SourceID, SourceVersion: row.SourceVersion, SubjectHash: row.SubjectHash, AdoptionUnit: append(json.RawMessage(nil), row.AdoptionUnit...), Dynamic: row.Dynamic, CandidateID: row.CandidateID}
}
func (s *Service) UpdateTestMetadata(ctx context.Context, userID, testID uint, req MetadataRequest) (TestDescriptor, error) {
	tags, _ := compute.CanonicalJSON(cleanStrings(req.Tags))
	updates := map[string]any{"notes": strings.TrimSpace(req.Notes), "tags": tags}
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	r := s.db.WithContext(ctx).Model(&saasstore.PerturbationTest{}).Where("id=? AND owner_user_id=?", testID, userID).Updates(updates)
	if r.Error != nil {
		return TestDescriptor{}, r.Error
	}
	if r.RowsAffected == 0 {
		return TestDescriptor{}, ErrNotFound
	}
	return s.GetTest(ctx, userID, testID, true)
}
func (s *Service) ArchiveTest(ctx context.Context, userID, testID uint) error {
	now := time.Now().UTC()
	r := s.db.WithContext(ctx).Model(&saasstore.PerturbationTest{}).Where("id=? AND owner_user_id=? AND archived_at IS NULL", testID, userID).Update("archived_at", now)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
