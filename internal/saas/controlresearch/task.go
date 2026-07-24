package controlresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/controlresearch"
	performancecore "quantsaas/internal/performance"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) StartNext(ctx context.Context, userID, taskID uint) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", taskID, userID).First(&task).Error; err != nil {
		return TaskDescriptor{}, ErrNotFound
	}
	if _, err := s.startNextStage(ctx, userID, task); err != nil {
		return TaskDescriptor{}, err
	}
	return s.Get(ctx, userID, task.ID)
}

func (s *Service) startNextStage(ctx context.Context, userID uint, task saasstore.ControlAnalysisTask) (*computetask.TaskDescriptor, error) {
	if task.ComputeTaskID == nil {
		return nil, ErrInvalidRequest
	}
	if err := s.syncTask(ctx, userID, &task); err != nil {
		return nil, err
	}
	var stages []saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("parent_task_id = ? AND user_id = ?", *task.ComputeTaskID, userID).Order("stage_order ASC,id ASC").Find(&stages).Error; err != nil {
		return nil, err
	}
	for _, stage := range stages {
		if stage.Status == compute.TaskStatusQueued || stage.Status == compute.TaskStatusRunning {
			return s.computeTasks.Get(ctx, userID, stage.ID)
		}
		if stage.Status == compute.TaskStatusPlanned || stage.Status == compute.TaskStatusPartial {
			started, err := s.computeTasks.StartTask(ctx, userID, stage.ID)
			if errors.Is(err, computetask.ErrDependencyPending) {
				continue
			}
			if err != nil {
				return nil, err
			}
			_ = s.db.WithContext(ctx).Model(&task).Update("status", "running").Error
			return started, nil
		}
	}
	return nil, nil
}

func (s *Service) Cancel(ctx context.Context, userID, taskID uint) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", taskID, userID).First(&task).Error; err != nil || task.ComputeTaskID == nil {
		return TaskDescriptor{}, ErrNotFound
	}
	if _, err := s.computeTasks.Cancel(ctx, userID, *task.ComputeTaskID); err != nil && !errors.Is(err, computetask.ErrInvalidState) {
		return TaskDescriptor{}, err
	}
	if err := s.syncTask(ctx, userID, &task); err != nil {
		return TaskDescriptor{}, err
	}
	return s.Get(ctx, userID, task.ID)
}

func (s *Service) Retry(ctx context.Context, userID, taskID uint) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", taskID, userID).First(&task).Error; err != nil || task.ComputeTaskID == nil {
		return TaskDescriptor{}, ErrNotFound
	}
	if _, err := s.computeTasks.Retry(ctx, userID, *task.ComputeTaskID); err != nil {
		return TaskDescriptor{}, err
	}
	return s.StartNext(ctx, userID, task.ID)
}

func (s *Service) syncTask(ctx context.Context, userID uint, task *saasstore.ControlAnalysisTask) error {
	if task.ComputeTaskID == nil {
		return nil
	}
	unlock := s.lockTaskSync(task.ID)
	defer unlock()
	root, err := s.computeTasks.Get(ctx, userID, *task.ComputeTaskID)
	if err != nil {
		return err
	}
	var canonical TaskCanonical
	if err := json.Unmarshal(task.Canonical, &canonical); err != nil {
		return err
	}
	var stages []saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("parent_task_id = ?", root.ID).Find(&stages).Error; err != nil {
		return err
	}
	var existing []saasstore.ControlEvaluation
	if err := s.db.WithContext(ctx).Select("kind", "sequence_index").Where("task_id = ?", task.ID).Find(&existing).Error; err != nil {
		return err
	}
	synced := make(map[string]struct{}, len(existing))
	for _, evaluation := range existing {
		synced[evaluationIdentity(evaluation.Kind, evaluation.SequenceIndex)] = struct{}{}
	}
	newResults := false
	for _, stage := range stages {
		if stage.Status == compute.TaskStatusPlanned && stage.StartedAt == nil {
			continue
		}
		var items []saasstore.ComputeTaskItem
		if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", stage.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			kind, index, err := evaluationIdentityForItem(stage.StageKey, item)
			if err != nil {
				return err
			}
			identity := evaluationIdentity(kind, index)
			if _, ok := synced[identity]; ok {
				continue
			}
			if err := s.syncItem(ctx, *task, canonical, stage.StageKey, item); err != nil {
				return err
			}
			synced[identity] = struct{}{}
			newResults = true
		}
	}
	if newResults || task.LatestSnapshotID == nil {
		if err := s.createSnapshot(ctx, task, root); err != nil {
			return err
		}
	}
	status := "running"
	var baseline, randomCount, ruleCount, shuffleCount int64
	s.db.WithContext(ctx).Model(&saasstore.ControlEvaluation{}).Where("task_id = ? AND kind = ?", task.ID, "baseline").Count(&baseline)
	s.db.WithContext(ctx).Model(&saasstore.ControlEvaluation{}).Where("task_id = ? AND kind = ?", task.ID, "random").Count(&randomCount)
	s.db.WithContext(ctx).Model(&saasstore.ControlEvaluation{}).Where("task_id = ? AND kind = ?", task.ID, "rule").Count(&ruleCount)
	s.db.WithContext(ctx).Model(&saasstore.ControlEvaluation{}).Where("task_id = ? AND kind = ?", task.ID, "shuffle").Count(&shuffleCount)
	complete := baseline == 1 && int(randomCount) >= task.RandomTargetCount && ruleCount == 4 && int(shuffleCount) >= task.ShuffleTargetCount
	if complete {
		status = "completed"
	} else if root.Status == compute.TaskStatusFailed && baseline+randomCount+ruleCount+shuffleCount == 0 {
		status = "failed"
	} else if baseline+randomCount+ruleCount+shuffleCount > 0 && (root.Status == compute.TaskStatusPartial || root.Status == compute.TaskStatusCancelled || root.Status == compute.TaskStatusFailed) {
		status = "partially_completed"
	} else if root.Status == compute.TaskStatusPlanned {
		status = "planned"
	}
	updates := map[string]any{"status": status}
	if complete {
		now := time.Now().UTC()
		updates["completed_at"] = now
	}
	if err := s.db.WithContext(ctx).Model(task).Updates(updates).Error; err != nil {
		return err
	}
	task.Status = status
	return s.updateCandidateLink(ctx, userID, *task)
}

func evaluationIdentity(kind string, index int) string {
	return kind + ":" + strconv.Itoa(index)
}

func evaluationIdentityForItem(stageKey string, item saasstore.ComputeTaskItem) (string, int, error) {
	switch stageKey {
	case "baseline":
		return "baseline", 0, nil
	case "random":
		return "random", parseItemIndex(item.ItemKey), nil
	default:
		var result ExecutionResult
		if err := json.Unmarshal(item.Result, &result); err != nil || result.SchemaVersion != ExecutorResultVersion {
			return "", 0, ErrInvalidRequest
		}
		return result.Kind, result.SequenceIndex, nil
	}
}

func (s *Service) syncItem(ctx context.Context, task saasstore.ControlAnalysisTask, canonical TaskCanonical, stageKey string, item saasstore.ComputeTaskItem) error {
	kind, index, ruleType := "", 0, ""
	var resultID uint
	var version, contentHash string
	switch stageKey {
	case "baseline", "random":
		if stageKey == "baseline" {
			kind = "baseline"
		} else {
			kind = "random"
			index = parseItemIndex(item.ItemKey)
		}
		if canonical.BaselineExecutorType == dynamicparamsvc.MaterializeExecutorType {
			var result dynamicparamsvc.MaterializeExecutionResult
			if json.Unmarshal(item.Result, &result) != nil || result.SchemaVersion != dynamicparamsvc.MaterializeResultVersion {
				return ErrInvalidRequest
			}
			resultID, version, contentHash = result.BacktestResultID, result.BacktestResultVersion, result.BacktestResultContentHash
		} else {
			var result robustnesssvc.PointExecutionResult
			if json.Unmarshal(item.Result, &result) != nil || result.SchemaVersion != robustnesssvc.PointResultVersion {
				return ErrInvalidRequest
			}
			resultID, version, contentHash = result.BacktestResultID, result.BacktestResultVersion, result.BacktestResultContentHash
		}
	default:
		var result ExecutionResult
		if json.Unmarshal(item.Result, &result) != nil || result.SchemaVersion != ExecutorResultVersion {
			return ErrInvalidRequest
		}
		kind, index, ruleType = result.Kind, result.SequenceIndex, result.RuleType
		resultID, version, contentHash = result.BacktestResultID, result.BacktestResultVersion, result.BacktestResultContentHash
	}
	metrics, err := s.metricsForResult(ctx, resultID)
	if err != nil {
		return err
	}
	metricsRaw, _ := compute.CanonicalJSON(metrics)
	evaluation := saasstore.ControlEvaluation{TaskID: task.ID, Kind: kind, SequenceIndex: index, RuleType: ruleType, BacktestResultID: resultID, BacktestResultVersion: version, BacktestResultContentHash: contentHash, Summary: metricsRaw, SummaryHash: compute.HashBytes(metricsRaw)}
	if kind == "random" {
		var record saasstore.RandomParameterRecord
		if err := s.db.WithContext(ctx).Where("batch_id = ? AND sequence_index = ?", task.RandomBatchID, index).First(&record).Error; err != nil {
			return err
		}
		evaluation.RandomParameterRecordID = &record.ID
		if err := s.db.WithContext(ctx).Model(&record).Updates(map[string]any{"backtest_result_id": resultID, "backtest_result_version": version, "backtest_content_hash": contentHash}).Error; err != nil {
			return err
		}
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_id"}, {Name: "kind"}, {Name: "sequence_index"}}, DoUpdates: clause.AssignmentColumns([]string{"rule_type", "random_parameter_record_id", "backtest_result_id", "backtest_result_version", "backtest_result_content_hash", "summary", "summary_hash", "updated_at"})}).Create(&evaluation).Error
}

func (s *Service) metricsForResult(ctx context.Context, resultID uint) (MetricSet, error) {
	if _, err := s.results.VerifyResult(ctx, resultID); err != nil {
		return MetricSet{}, err
	}
	loaded, err := s.results.Load(ctx, resultID, true)
	if err != nil || loaded.Summary == nil {
		return MetricSet{}, err
	}
	var practical struct {
		TotalReturn *float64 `json:"practical_total_return"`
		MaxDrawdown *float64 `json:"practical_max_drawdown"`
		FinalEquity *float64 `json:"practical_final_equity"`
		TradeCount  *int     `json:"practical_trade_count"`
	}
	_ = json.Unmarshal(loaded.Summary.Extra, &practical)
	points := make([]performancecore.Point, 0)
	for _, block := range loaded.Blocks {
		for _, point := range block.Points {
			benchmark := point.TotalEquity
			if point.BenchmarkEquity != nil {
				benchmark = *point.BenchmarkEquity
			}
			points = append(points, performancecore.Point{TimeMs: point.TimeMs, NAV: point.PracticalTotalEquity, BenchmarkNAV: benchmark, ActualExposure: point.PracticalActualExposureWeight})
		}
	}
	analysis, err := performancecore.Analyze(points, points, nil, performancecore.Config{RiskFreeAnnualRate: 0, HistogramBins: performancecore.DefaultHistogramBins})
	if err != nil {
		return MetricSet{}, err
	}
	roi := loaded.Summary.ROI
	maxDrawdown := loaded.Summary.MaxDrawdown
	finalEquity := loaded.Summary.FinalEquity
	tradeCount := loaded.Summary.TradeCount
	if practical.TotalReturn != nil {
		roi = *practical.TotalReturn
	}
	if practical.MaxDrawdown != nil {
		maxDrawdown = *practical.MaxDrawdown
	}
	if practical.FinalEquity != nil {
		finalEquity = *practical.FinalEquity
	}
	if practical.TradeCount != nil {
		tradeCount = *practical.TradeCount
	}
	return MetricSet{ROI: roi, FinalEquity: finalEquity, FinalNAVRatio: analysis.Summary.Relative.FinalNAVRatio, LogFinalNAVRatio: analysis.Summary.Relative.LogFinalNAVRatio, MaxDrawdown: maxDrawdown, Sortino: analysis.Summary.Sortino.Value, LongestUnderwaterDays: analysis.Summary.LongestUnderwater.LongestDays, TradeCount: tradeCount, ExposureDaysRatio: analysis.Summary.Exposure.ExposureDaysRatio, AverageExposure: analysis.Summary.Exposure.AverageActualExposure, FeeCost: loaded.Summary.Costs.FeeCost, SlippageCost: loaded.Summary.Costs.SlippageCost, ReturnDistributions: analysis.Summary.Distributions}, nil
}

func (s *Service) createSnapshot(ctx context.Context, task *saasstore.ControlAnalysisTask, root *computetask.TaskDescriptor) error {
	var evaluations []saasstore.ControlEvaluation
	if err := s.db.WithContext(ctx).Where("task_id = ?", task.ID).Order("kind ASC,sequence_index ASC").Find(&evaluations).Error; err != nil {
		return err
	}
	var baseline *saasstore.ControlEvaluation
	random, rules, shuffle := []saasstore.ControlEvaluation{}, []saasstore.ControlEvaluation{}, []saasstore.ControlEvaluation{}
	for i := range evaluations {
		switch evaluations[i].Kind {
		case "baseline":
			baseline = &evaluations[i]
		case "random":
			random = append(random, evaluations[i])
		case "rule":
			rules = append(rules, evaluations[i])
		case "shuffle":
			shuffle = append(shuffle, evaluations[i])
		}
	}
	if baseline == nil {
		return nil
	}
	identity := make([]map[string]any, 0, len(evaluations))
	for _, evaluation := range evaluations {
		identity = append(identity, map[string]any{"id": evaluation.ID, "kind": evaluation.Kind, "index": evaluation.SequenceIndex, "hash": evaluation.BacktestResultContentHash, "summary_hash": evaluation.SummaryHash})
	}
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"task_id": task.ID, "random_target": task.RandomTargetCount, "shuffle_target": task.ShuffleTargetCount, "evaluations": identity, "statistics_version": core.StatisticsVersion})
	snapshotKey := "p11-snapshot:" + compute.HashBytes(identityRaw)
	var existing saasstore.ControlAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("snapshot_key = ?", snapshotKey).First(&existing).Error; err == nil {
		return s.promoteLatestSnapshot(ctx, task, existing)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var baselineMetrics MetricSet
	if err := json.Unmarshal(baseline.Summary, &baselineMetrics); err != nil {
		return err
	}
	summary := SnapshotSummary{SchemaVersion: SnapshotSchemaVersion, BaselineEvaluationID: baseline.ID, BaselineResultID: baseline.BacktestResultID, Baseline: baselineMetrics, Rules: []RuleResult{}, ConclusionLabels: []string{}}
	if len(random) > 0 {
		distribution, percentiles, err := distributionAndPercentiles(baselineMetrics, random)
		if err != nil {
			return err
		}
		summary.RandomDistribution = &distribution
		summary.RandomPercentiles = &percentiles
		summary.ConclusionLabels = append(summary.ConclusionLabels, conclusionLabel("同結構隨機參數分佈", percentiles))
	}
	if len(shuffle) > 0 {
		distribution, percentiles, err := distributionAndPercentiles(baselineMetrics, shuffle)
		if err != nil {
			return err
		}
		summary.ShuffleDistribution = &distribution
		summary.ShufflePercentiles = &percentiles
		summary.ConclusionLabels = append(summary.ConclusionLabels, conclusionLabel("曝險順序打亂分佈", percentiles))
	}
	for _, evaluation := range rules {
		var metrics MetricSet
		if err := json.Unmarshal(evaluation.Summary, &metrics); err != nil {
			return err
		}
		summary.Rules = append(summary.Rules, RuleResult{EvaluationID: evaluation.ID, RuleType: evaluation.RuleType, BacktestResultID: evaluation.BacktestResultID, Metrics: metrics})
	}
	representatives := representativeRoles(append(append([]saasstore.ControlEvaluation{}, random...), shuffle...))
	manifest := make([]map[string]any, 0, len(evaluations))
	for _, evaluation := range evaluations {
		manifest = append(manifest, map[string]any{"evaluation_id": evaluation.ID, "kind": evaluation.Kind, "sequence_index": evaluation.SequenceIndex, "backtest_result_id": evaluation.BacktestResultID, "result_version": evaluation.BacktestResultVersion, "result_content_hash": evaluation.BacktestResultContentHash, "representative_role": representatives[evaluation.ID]})
	}
	summaryRaw, _ := compute.CanonicalJSON(summary)
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	completeness := "partially_completed"
	if len(random) >= task.RandomTargetCount && len(rules) == 4 && len(shuffle) >= task.ShuffleTargetCount {
		completeness = "completed"
	}
	snapshotEnvelope, _ := compute.CanonicalJSON(map[string]any{"schema_version": SnapshotSchemaVersion, "task_id": task.ID, "completeness": completeness, "summary": json.RawMessage(summaryRaw), "manifest": json.RawMessage(manifestRaw), "statistics_version": core.StatisticsVersion})
	snapshot := saasstore.ControlAnalysisSnapshot{TaskID: task.ID, SnapshotKey: snapshotKey, SchemaVersion: SnapshotSchemaVersion, Completeness: completeness, StatisticsVersion: core.StatisticsVersion, RandomCompletedCount: len(random), ShuffleCompletedCount: len(shuffle), RuleCompletedCount: len(rules), FailedCount: root.FailedCount, CancelledCount: root.CancelledCount, CacheHitCount: root.CacheHitCount, Summary: summaryRaw, DetailManifest: manifestRaw, ContentHash: compute.HashBytes(snapshotEnvelope)}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_key"}}, DoNothing: true}).Create(&snapshot)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("snapshot_key = ?", snapshotKey).First(&snapshot).Error; err != nil {
				return err
			}
			return promoteLatestSnapshotTx(tx, task, snapshot)
		}
		for _, evaluation := range evaluations {
			member := saasstore.ControlSnapshotMember{SnapshotID: snapshot.ID, EvaluationID: evaluation.ID, RepresentativeRole: representatives[evaluation.ID]}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
		}
		return promoteLatestSnapshotTx(tx, task, snapshot)
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) promoteLatestSnapshot(ctx context.Context, task *saasstore.ControlAnalysisTask, candidate saasstore.ControlAnalysisSnapshot) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return promoteLatestSnapshotTx(tx, task, candidate)
	})
}

func promoteLatestSnapshotTx(tx *gorm.DB, task *saasstore.ControlAnalysisTask, candidate saasstore.ControlAnalysisSnapshot) error {
	var locked saasstore.ControlAnalysisTask
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "latest_snapshot_id").First(&locked, task.ID).Error; err != nil {
		return err
	}
	if locked.LatestSnapshotID != nil {
		var current saasstore.ControlAnalysisSnapshot
		if err := tx.First(&current, *locked.LatestSnapshotID).Error; err != nil {
			return err
		}
		if !snapshotAtLeastAsComplete(candidate, current) {
			task.LatestSnapshotID = locked.LatestSnapshotID
			return nil
		}
	}
	if err := tx.Model(&saasstore.ControlAnalysisTask{}).Where("id = ?", task.ID).Update("latest_snapshot_id", candidate.ID).Error; err != nil {
		return err
	}
	task.LatestSnapshotID = &candidate.ID
	return nil
}

func snapshotAtLeastAsComplete(candidate, current saasstore.ControlAnalysisSnapshot) bool {
	if candidate.RandomCompletedCount < current.RandomCompletedCount ||
		candidate.RuleCompletedCount < current.RuleCompletedCount ||
		candidate.ShuffleCompletedCount < current.ShuffleCompletedCount {
		return false
	}
	if current.Completeness == "completed" && candidate.Completeness != "completed" {
		return false
	}
	return true
}

func distributionAndPercentiles(baseline MetricSet, evaluations []saasstore.ControlEvaluation) (DistributionSet, PercentileSet, error) {
	logs, drawdowns, sortinos, underwater := []float64{}, []float64{}, []float64{}, []float64{}
	for _, evaluation := range evaluations {
		var metrics MetricSet
		if err := json.Unmarshal(evaluation.Summary, &metrics); err != nil {
			return DistributionSet{}, PercentileSet{}, err
		}
		logs = append(logs, metrics.LogFinalNAVRatio)
		drawdowns = append(drawdowns, metrics.MaxDrawdown)
		underwater = append(underwater, metrics.LongestUnderwaterDays)
		if metrics.Sortino != nil {
			sortinos = append(sortinos, *metrics.Sortino)
		}
	}
	logD, _ := core.Summarize(logs)
	ddD, _ := core.Summarize(drawdowns)
	uwD, _ := core.Summarize(underwater)
	logP, _ := core.Percentile(baseline.LogFinalNAVRatio, logs, true)
	ddP, _ := core.Percentile(baseline.MaxDrawdown, drawdowns, false)
	uwP, _ := core.Percentile(baseline.LongestUnderwaterDays, underwater, false)
	distribution := DistributionSet{LogFinalNAVRatio: logD, MaxDrawdown: ddD, LongestUnderwaterDays: uwD}
	percentiles := PercentileSet{LogFinalNAVRatio: logP, MaxDrawdown: ddP, LongestUnderwaterDays: uwP}
	if baseline.Sortino != nil && len(sortinos) > 0 {
		d, _ := core.Summarize(sortinos)
		p, _ := core.Percentile(*baseline.Sortino, sortinos, true)
		distribution.Sortino = &d
		percentiles.Sortino = &p
	}
	return distribution, percentiles, nil
}

func conclusionLabel(comparison string, percentiles PercentileSet) string {
	metrics := []string{
		fmt.Sprintf("報酬第 %.1f 百分位", percentiles.LogFinalNAVRatio),
		fmt.Sprintf("最大回撤第 %.1f 百分位", percentiles.MaxDrawdown),
	}
	if percentiles.Sortino != nil {
		metrics = append(metrics, fmt.Sprintf("Sortino 第 %.1f 百分位", *percentiles.Sortino))
	}
	return fmt.Sprintf("評估對象於%s：%s", comparison, strings.Join(metrics, "、"))
}

func representativeRoles(evaluations []saasstore.ControlEvaluation) map[uint]string {
	byKind := map[string][]saasstore.ControlEvaluation{}
	for _, evaluation := range evaluations {
		byKind[evaluation.Kind] = append(byKind[evaluation.Kind], evaluation)
	}
	result := map[uint]string{}
	for kind, items := range byKind {
		sort.Slice(items, func(i, j int) bool {
			var a, b MetricSet
			_ = json.Unmarshal(items[i].Summary, &a)
			_ = json.Unmarshal(items[j].Summary, &b)
			return a.LogFinalNAVRatio < b.LogFinalNAVRatio
		})
		if len(items) == 0 {
			continue
		}
		if len(items) == 1 {
			result[items[0].ID] = kind + "_median"
			continue
		}
		assignments := map[uint][]string{}
		assignments[items[0].ID] = append(assignments[items[0].ID], kind+"_low")
		assignments[items[len(items)/2].ID] = append(assignments[items[len(items)/2].ID], kind+"_median")
		assignments[items[len(items)-1].ID] = append(assignments[items[len(items)-1].ID], kind+"_high")
		for id, roles := range assignments {
			result[id] = strings.Join(roles, ",")
		}
	}
	return result
}

func parseItemIndex(key string) int {
	parts := strings.Split(key, ":")
	if len(parts) < 2 {
		return 0
	}
	value, _ := strconv.Atoi(parts[len(parts)-1])
	return value
}

func (s *Service) updateCandidateLink(ctx context.Context, userID uint, task saasstore.ControlAnalysisTask) error {
	if task.CandidateID == nil {
		return nil
	}
	status := "running"
	switch task.Status {
	case "completed":
		status = "completed"
	case "partially_completed":
		status = "partially_completed"
	case "failed":
		status = "failed"
	}
	partial := json.RawMessage(`{}`)
	sourceHash := ""
	if task.LatestSnapshotID != nil {
		var snapshot saasstore.ControlAnalysisSnapshot
		if s.db.WithContext(ctx).First(&snapshot, *task.LatestSnapshotID).Error == nil {
			sourceHash = snapshot.ContentHash
			partial, _ = compute.CanonicalJSON(map[string]any{"snapshot_id": snapshot.ID, "completeness": snapshot.Completeness, "random_completed": snapshot.RandomCompletedCount, "shuffle_completed": snapshot.ShuffleCompletedCount, "rules_completed": snapshot.RuleCompletedCount})
		}
	}
	_, err := s.parameterResearch.UpdateAnalysisLink(ctx, userID, *task.CandidateID, "H", parameterresearchsvc.UpdateAnalysisLinkRequest{Status: status, TaskID: &task.ID, SourceID: fmt.Sprintf("%d", task.ID), SourceVersion: SnapshotSchemaVersion, SourceContentHash: sourceHash, PartialSnapshot: partial})
	return err
}

func (s *Service) extensionPlan(ctx context.Context, userID uint, task saasstore.ControlAnalysisTask, randomCount, shuffleCount int) (preparedPlan, error) {
	if randomCount < task.RandomTargetCount || shuffleCount < task.ShuffleTargetCount || (randomCount == task.RandomTargetCount && shuffleCount == task.ShuffleTargetCount) {
		return preparedPlan{}, ErrExtensionCountNotIncreased
	}
	var canonical TaskCanonical
	if json.Unmarshal(task.Canonical, &canonical) != nil {
		return preparedPlan{}, ErrInvalidRequest
	}
	var validator func(map[string]float64) error
	var err error
	if canonical.ResearchConfigurationID != 0 {
		validator, err = s.parameterResearch.BuildPointValidator(ctx, userID, canonical.ResearchConfigurationID)
	} else {
		var gene saasstore.GeneRecord
		if err = s.db.WithContext(ctx).First(&gene, canonical.SourceGenomeID).Error; err == nil {
			params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
			validator = func(values map[string]float64) error {
				_, e := robust.ChromosomeWithValues(params.Chromosome, values)
				return e
			}
		}
	}
	if err != nil {
		return preparedPlan{}, err
	}
	batch, err := core.GenerateDiscrete(canonical.ParameterSpace, randomCount, canonical.RandomSeed, validator)
	if err != nil {
		return preparedPlan{}, err
	}
	composite, err := s.buildComposite(ctx, userID, task.TaskKey, task.CanonicalHash, canonical, batch, randomCount, shuffleCount)
	if err != nil {
		return preparedPlan{}, err
	}
	preview, err := s.computeTasks.PreviewComposite(ctx, userID, composite)
	return preparedPlan{canonical: canonical, canonicalRaw: task.Canonical, canonicalHash: task.CanonicalHash, taskKey: task.TaskKey, batchKey: "", batch: batch, composite: composite, preview: preview}, err
}

func (s *Service) PreviewExtension(ctx context.Context, userID, taskID uint, request ExtendRequest) (PlanResponse, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return PlanResponse{}, ErrNotFound
	}
	prepared, err := s.extensionPlan(ctx, userID, task, request.RandomCount, request.ShuffleCount)
	if err != nil {
		return PlanResponse{}, err
	}
	prepared.batchKey = "existing"
	return planResponse(prepared), nil
}

func (s *Service) Extend(ctx context.Context, userID, taskID uint, request ExtendRequest) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return TaskDescriptor{}, ErrNotFound
	}
	prepared, err := s.extensionPlan(ctx, userID, task, request.RandomCount, request.ShuffleCount)
	if err != nil {
		return TaskDescriptor{}, err
	}
	var batch saasstore.RandomParameterBatch
	if err = s.db.WithContext(ctx).First(&batch, task.RandomBatchID).Error; err != nil {
		return TaskDescriptor{}, err
	}
	prepared.batchKey = batch.BatchKey
	if _, err = s.persistBatch(ctx, userID, prepared); err != nil {
		return TaskDescriptor{}, err
	}
	root, err := s.computeTasks.CreateComposite(ctx, userID, prepared.composite, request.ConfirmSoftLimit)
	if err != nil {
		return TaskDescriptor{}, err
	}
	if err = s.db.WithContext(ctx).Model(&task).Updates(map[string]any{"random_target_count": request.RandomCount, "shuffle_target_count": request.ShuffleCount, "compute_task_id": root.ID, "status": "supplementing", "completed_at": nil}).Error; err != nil {
		return TaskDescriptor{}, err
	}
	task.RandomTargetCount = request.RandomCount
	task.ShuffleTargetCount = request.ShuffleCount
	task.ComputeTaskID = &root.ID
	if _, err = s.startNextStage(ctx, userID, task); err != nil {
		return TaskDescriptor{}, err
	}
	return s.Get(ctx, userID, task.ID)
}
