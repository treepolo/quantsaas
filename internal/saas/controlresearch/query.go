package controlresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/saas/backtestresult"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

func (s *Service) List(ctx context.Context, userID uint, limit int) ([]TaskDescriptor, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var rows []saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", userID).Order("created_at DESC,id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]TaskDescriptor, 0, len(rows))
	for i := range rows {
		if err := s.syncTask(ctx, userID, &rows[i]); err != nil {
			return nil, err
		}
		descriptor, err := s.describeTask(ctx, rows[i])
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, userID, taskID uint) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", taskID, userID).First(&task).Error; err != nil {
		return TaskDescriptor{}, ErrNotFound
	}
	if err := s.syncTask(ctx, userID, &task); err != nil {
		return TaskDescriptor{}, err
	}
	if err := s.db.WithContext(ctx).First(&task, task.ID).Error; err != nil {
		return TaskDescriptor{}, err
	}
	return s.describeTask(ctx, task)
}

func (s *Service) describeTask(ctx context.Context, task saasstore.ControlAnalysisTask) (TaskDescriptor, error) {
	var tags []string
	_ = json.Unmarshal(task.Tags, &tags)
	descriptor := TaskDescriptor{ID: task.ID, Name: task.Name, Notes: task.Notes, Tags: tags, Status: task.Status, SourceKind: task.SourceKind, SourceGenomeID: task.SourceGenomeID, CandidateID: task.CandidateID, ResearchConfigurationID: task.ResearchConfigurationID, RandomBatchID: task.RandomBatchID, RandomTargetCount: task.RandomTargetCount, ShuffleTargetCount: task.ShuffleTargetCount, ToggleEveryNBars: task.ToggleEveryNBars, SameStructure: task.ResearchConfigurationID != nil, ComputeTaskID: task.ComputeTaskID, Archived: task.ArchivedAt != nil, CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339)}
	if task.CompletedAt != nil {
		descriptor.CompletedAt = task.CompletedAt.UTC().Format(time.RFC3339)
	}
	if task.ComputeTaskID != nil {
		var stages []saasstore.ComputeTask
		if err := s.db.WithContext(ctx).Where("parent_task_id = ?", *task.ComputeTaskID).Order("stage_order ASC,id ASC").Find(&stages).Error; err != nil {
			return descriptor, err
		}
		for _, stage := range stages {
			descriptor.Stages = append(descriptor.Stages, StageDescriptor{ID: stage.ID, Key: stage.StageKey, Type: stage.StageType, Status: stage.Status, CompletedCount: stage.ValidResultCount, TotalCount: stage.TotalItems, FailedCount: stage.FailedCount, Progress: stage.Progress, Error: stage.ErrorMessage})
		}
	}
	if task.LatestSnapshotID != nil {
		snapshot, err := s.snapshotDescriptor(ctx, *task.LatestSnapshotID)
		if err != nil {
			return descriptor, err
		}
		descriptor.LatestSnapshot = &snapshot
	}
	return descriptor, nil
}

func (s *Service) Snapshots(ctx context.Context, userID, taskID uint) ([]SnapshotDescriptor, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&saasstore.ControlAnalysisTask{}).Where("id=? AND owner_user_id=?", taskID, userID).Count(&count).Error; err != nil || count != 1 {
		return nil, ErrNotFound
	}
	var rows []saasstore.ControlAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("task_id=?", taskID).Order("created_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]SnapshotDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor, err := s.snapshotDescriptorFrom(row)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) snapshotDescriptor(ctx context.Context, id uint) (SnapshotDescriptor, error) {
	var row saasstore.ControlAnalysisSnapshot
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return SnapshotDescriptor{}, ErrNotFound
	}
	return s.snapshotDescriptorFrom(row)
}
func (s *Service) snapshotDescriptorFrom(row saasstore.ControlAnalysisSnapshot) (SnapshotDescriptor, error) {
	var summary SnapshotSummary
	if err := json.Unmarshal(row.Summary, &summary); err != nil {
		return SnapshotDescriptor{}, err
	}
	if summary.Rules == nil {
		summary.Rules = []RuleResult{}
	}
	if summary.ConclusionLabels == nil {
		summary.ConclusionLabels = []string{}
	}
	return SnapshotDescriptor{ID: row.ID, Completeness: row.Completeness, StatisticsVersion: row.StatisticsVersion, RandomCompletedCount: row.RandomCompletedCount, ShuffleCompletedCount: row.ShuffleCompletedCount, RuleCompletedCount: row.RuleCompletedCount, FailedCount: row.FailedCount, CancelledCount: row.CancelledCount, CacheHitCount: row.CacheHitCount, ContentHash: row.ContentHash, Summary: summary, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)}, nil
}

func (s *Service) RandomRecords(ctx context.Context, userID, batchID uint, limit, offset int) ([]RandomRecordDescriptor, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var batch saasstore.RandomParameterBatch
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", batchID, userID).First(&batch).Error; err != nil {
		return nil, ErrNotFound
	}
	var rows []saasstore.RandomParameterRecord
	if err := s.db.WithContext(ctx).Where("batch_id=?", batch.ID).Order("sequence_index ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]RandomRecordDescriptor, 0, len(rows))
	for _, row := range rows {
		var coordinates []int
		var parameters map[string]float64
		_ = json.Unmarshal(row.Coordinates, &coordinates)
		_ = json.Unmarshal(row.Parameters, &parameters)
		result = append(result, RandomRecordDescriptor{ID: row.ID, BatchID: row.BatchID, SequenceIndex: row.SequenceIndex, Coordinates: coordinates, Parameters: parameters, ContentHash: row.ContentHash, BacktestResultID: row.BacktestResultID, BacktestResultVersion: row.BacktestResultVersion, BacktestContentHash: row.BacktestContentHash})
	}
	return result, nil
}

func (s *Service) Detail(ctx context.Context, userID, taskID, snapshotID uint) (DetailDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return DetailDescriptor{}, ErrNotFound
	}
	var snapshot saasstore.ControlAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("id=? AND task_id=?", snapshotID, task.ID).First(&snapshot).Error; err != nil {
		return DetailDescriptor{}, ErrNotFound
	}
	var members []saasstore.ControlSnapshotMember
	if err := s.db.WithContext(ctx).Where("snapshot_id=?", snapshot.ID).Find(&members).Error; err != nil {
		return DetailDescriptor{}, err
	}
	roles := map[uint]string{}
	ids := make([]uint, 0, len(members))
	for _, member := range members {
		roles[member.EvaluationID] = member.RepresentativeRole
		ids = append(ids, member.EvaluationID)
	}
	var evaluations []saasstore.ControlEvaluation
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&evaluations).Error; err != nil {
			return DetailDescriptor{}, err
		}
	}
	sort.Slice(evaluations, func(i, j int) bool {
		if evaluations[i].Kind == evaluations[j].Kind {
			return evaluations[i].SequenceIndex < evaluations[j].SequenceIndex
		}
		return evaluations[i].Kind < evaluations[j].Kind
	})
	result := DetailDescriptor{SchemaVersion: DetailSchemaVersion, TaskID: task.ID, SnapshotID: snapshot.ID}
	for _, row := range evaluations {
		var metrics MetricSet
		if err := json.Unmarshal(row.Summary, &metrics); err != nil {
			return result, err
		}
		result.Evaluations = append(result.Evaluations, EvaluationDescriptor{ID: row.ID, Kind: row.Kind, SequenceIndex: row.SequenceIndex, RuleType: row.RuleType, RandomParameterRecordID: row.RandomParameterRecordID, BacktestResultID: row.BacktestResultID, BacktestResultVersion: row.BacktestResultVersion, BacktestResultContentHash: row.BacktestResultContentHash, Metrics: metrics, RepresentativeRole: roles[row.ID]})
	}
	return result, nil
}

func (s *Service) PathBlock(ctx context.Context, userID, evaluationID uint, blockIndex int) (PathBlockDescriptor, error) {
	var evaluation saasstore.ControlEvaluation
	if err := s.db.WithContext(ctx).Table("control_evaluations AS evaluation").Select("evaluation.*").Joins("JOIN control_analysis_tasks task ON task.id = evaluation.task_id").Where("evaluation.id=? AND task.owner_user_id=?", evaluationID, userID).Scan(&evaluation).Error; err != nil || evaluation.ID == 0 {
		return PathBlockDescriptor{}, ErrNotFound
	}
	block, model, err := s.results.LoadBlock(ctx, evaluation.BacktestResultID, blockIndex)
	if err != nil {
		return PathBlockDescriptor{}, err
	}
	return PathBlockDescriptor{EvaluationID: evaluation.ID, ResultID: evaluation.BacktestResultID, BlockIndex: blockIndex, ContentHash: model.ContentHash, Block: block}, nil
}

func (s *Service) Comparison(ctx context.Context, userID, taskID, snapshotID uint) (ComparisonDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return ComparisonDescriptor{}, ErrNotFound
	}
	var snapshot saasstore.ControlAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("id=? AND task_id=?", snapshotID, task.ID).First(&snapshot).Error; err != nil {
		return ComparisonDescriptor{}, ErrNotFound
	}
	return ComparisonDescriptor{SourceKind: "control_analysis_snapshot", SourceID: task.ID, SourceVersion: ComparisonSourceVersion, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash, DisplayName: task.Name, SourceStatus: snapshot.Completeness, Archived: task.ArchivedAt != nil, SourceLink: fmt.Sprintf("/research/control-analysis?task=%d&snapshot=%d", task.ID, snapshot.ID), AvailableBlocks: []string{"conditions", "baseline", "random_distribution", "meaningless_rules", "exposure_shuffle", "details"}}, nil
}

func (s *Service) UpdateMetadata(ctx context.Context, userID, taskID uint, request UpdateMetadataRequest) (TaskDescriptor, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return TaskDescriptor{}, ErrNotFound
	}
	updates := map[string]any{}
	if request.Name != "" {
		updates["name"] = request.Name
	}
	updates["notes"] = request.Notes
	tagsRaw, _ := compute.CanonicalJSON(cleanStrings(request.Tags))
	updates["tags"] = tagsRaw
	if request.Archived != nil {
		if *request.Archived {
			now := time.Now().UTC()
			updates["archived_at"] = now
			updates["status"] = "archived"
		} else {
			updates["archived_at"] = nil
			if task.Status == "archived" {
				updates["status"] = "partially_completed"
			}
		}
	}
	if err := s.db.WithContext(ctx).Model(&task).Updates(updates).Error; err != nil {
		return TaskDescriptor{}, err
	}
	return s.Get(ctx, userID, task.ID)
}

func (s *Service) DeleteImpact(ctx context.Context, userID, taskID uint) (map[string]any, error) {
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return nil, ErrNotFound
	}
	var evaluations []saasstore.ControlEvaluation
	if err := s.db.WithContext(ctx).Where("task_id = ?", task.ID).Find(&evaluations).Error; err != nil {
		return nil, err
	}
	resultIDs := make([]uint, 0, len(evaluations))
	references := map[uint]backtestresult.ReferenceReport{}
	seen := map[uint]bool{}
	for _, evaluation := range evaluations {
		if seen[evaluation.BacktestResultID] {
			continue
		}
		seen[evaluation.BacktestResultID] = true
		resultIDs = append(resultIDs, evaluation.BacktestResultID)
		report, err := s.results.References(ctx, evaluation.BacktestResultID)
		if err != nil {
			return nil, err
		}
		references[evaluation.BacktestResultID] = report
	}
	sort.Slice(resultIDs, func(i, j int) bool { return resultIDs[i] < resultIDs[j] })
	terminal := task.Status == "completed" || task.Status == "partially_completed" || task.Status == "failed" || task.Status == "archived"
	return map[string]any{
		"task_id": task.ID, "n_workspace_ids": []uint{}, "candidate_id": task.CandidateID, "random_batch_id": task.RandomBatchID,
		"backtest_result_ids": resultIDs, "backtest_references": references,
		"hard_delete_allowed": terminal, "path_delete_allowed": terminal,
		"message": "P15 尚未建立，因此目前沒有 N 工作區引用；刪除任務只移除 H 任務、評估與快照，不刪除共用標準回測或計算稽核紀錄。",
	}, nil
}

func (s *Service) DeleteTask(ctx context.Context, userID, taskID uint, confirm bool) (map[string]any, error) {
	impact, err := s.DeleteImpact(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	if !confirm || impact["hard_delete_allowed"] != true {
		return impact, ErrInvalidRequest
	}
	var task saasstore.ControlAnalysisTask
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", taskID, userID).First(&task).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var snapshotIDs []uint
		if err := tx.Model(&saasstore.ControlAnalysisSnapshot{}).Where("task_id = ?", task.ID).Pluck("id", &snapshotIDs).Error; err != nil {
			return err
		}
		if len(snapshotIDs) > 0 {
			if err := tx.Where("snapshot_id IN ?", snapshotIDs).Delete(&saasstore.ControlSnapshotMember{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("task_id = ?", task.ID).Delete(&saasstore.ControlAnalysisSnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", task.ID).Delete(&saasstore.ControlEvaluation{}).Error; err != nil {
			return err
		}
		return tx.Delete(&saasstore.ControlAnalysisTask{}, task.ID).Error
	}); err != nil {
		return nil, err
	}
	if task.CandidateID != nil {
		_, _ = s.parameterResearch.UpdateAnalysisLink(ctx, userID, *task.CandidateID, "H", parameterresearchsvc.UpdateAnalysisLinkRequest{Status: "not_calculated", PartialSnapshot: json.RawMessage(`{}`)})
	}
	return impact, nil
}

func (s *Service) DeletePathDetails(ctx context.Context, userID, taskID uint, confirm bool) (map[string]any, error) {
	impact, err := s.DeleteImpact(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	if !confirm || impact["path_delete_allowed"] != true {
		return impact, ErrInvalidRequest
	}
	ids, _ := impact["backtest_result_ids"].([]uint)
	deleted := []uint{}
	for _, id := range ids {
		if _, err := s.results.DeletePathDetail(ctx, id, true); err != nil {
			if errors.Is(err, backtestresult.ErrInvalidState) {
				continue
			}
			return impact, err
		}
		deleted = append(deleted, id)
	}
	impact["deleted_path_result_ids"] = deleted
	return impact, nil
}

func (s *Service) DeleteUnusedBatch(ctx context.Context, userID, batchID uint, confirm bool) (map[string]any, error) {
	var batch saasstore.RandomParameterBatch
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", batchID, userID).First(&batch).Error; err != nil {
		return nil, ErrNotFound
	}
	var taskIDs []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.ControlAnalysisTask{}).Where("random_batch_id = ?", batch.ID).Pluck("id", &taskIDs).Error; err != nil {
		return nil, err
	}
	impact := map[string]any{"batch_id": batch.ID, "task_ids": taskIDs, "delete_allowed": len(taskIDs) == 0}
	if !confirm || len(taskIDs) != 0 {
		return impact, ErrInvalidRequest
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("batch_id = ?", batch.ID).Delete(&saasstore.RandomParameterRecord{}).Error; err != nil {
			return err
		}
		return tx.Delete(&saasstore.RandomParameterBatch{}, batch.ID).Error
	}); err != nil {
		return nil, err
	}
	return impact, nil
}
