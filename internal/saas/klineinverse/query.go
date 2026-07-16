package klineinverse

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"

	core "quantsaas/internal/klineinverse"
	saasstore "quantsaas/internal/saas/store"
)

type PathQuery struct {
	Page      int
	PageSize  int
	CellIndex *int
	State     string
	Target    string
	Permanent *bool
}

func (s *Service) Overview(ctx context.Context, userID, studyID, snapshotID uint) (Overview, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return Overview{}, err
	}
	snapshot, err := s.loadSnapshot(ctx, study, snapshotID)
	if err != nil {
		return Overview{}, err
	}
	var result Overview
	if json.Unmarshal(snapshot.Summary, &result) != nil {
		return Overview{}, ErrInvalidRequest
	}
	result.SnapshotID = snapshot.ID
	result.Status = study.Status
	return result, nil
}

func (s *Service) Map(ctx context.Context, userID, studyID, snapshotID uint, axisX, axisY, target, color string) (MapResponse, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return MapResponse{}, err
	}
	if !validFeatureName(axisX) || !validFeatureName(axisY) || axisX == axisY {
		return MapResponse{}, ErrInvalidRequest
	}
	if target != "A" && target != "B" {
		return MapResponse{}, ErrInvalidRequest
	}
	allowedColors := []string{"evaluation_count", "target_count", "best_q_rel", "median_q_rel", "best_q_abs", "median_q_abs", "nearest_d_total", "active_pareto_count"}
	if !contains(allowedColors, color) {
		return MapResponse{}, ErrInvalidRequest
	}
	snapshot, err := s.loadSnapshot(ctx, study, snapshotID)
	if err != nil {
		return MapResponse{}, err
	}
	var cells []CellSummary
	if json.Unmarshal(snapshot.CellSummary, &cells) != nil {
		return MapResponse{}, ErrInvalidRequest
	}
	return MapResponse{StudyID: study.ID, SnapshotID: snapshot.ID, AxisX: axisX, AxisY: axisY, Target: target, Color: color, Cells: cells}, nil
}

func (s *Service) Paths(ctx context.Context, userID, studyID uint, query PathQuery) (PathPage, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return PathPage{}, err
	}
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 25
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	dbQuery := s.db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("study_id = ? AND status = ?", study.ID, "completed")
	if query.CellIndex != nil {
		dbQuery = dbQuery.Where("cell_index = ?", *query.CellIndex)
	}
	if strings.TrimSpace(query.State) != "" {
		dbQuery = dbQuery.Where("outcome_state = ?", strings.TrimSpace(query.State))
	}
	if query.Permanent != nil {
		dbQuery = dbQuery.Where("permanent = ?", *query.Permanent)
	}
	switch query.Target {
	case "A":
		dbQuery = dbQuery.Where("pass_a = ?", true)
	case "B":
		dbQuery = dbQuery.Where("pass_b = ?", true)
	case "", "all":
	default:
		return PathPage{}, ErrInvalidRequest
	}
	var total int64
	if err := dbQuery.Count(&total).Error; err != nil {
		return PathPage{}, err
	}
	var evaluations []saasstore.KlineInverseEvaluation
	if err := dbQuery.Order("permanent DESC, q_relative DESC, q_absolute DESC, id ASC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&evaluations).Error; err != nil {
		return PathPage{}, err
	}
	items := make([]PathSummary, 0, len(evaluations))
	for _, evaluation := range evaluations {
		item, err := s.pathSummary(ctx, evaluation)
		if err != nil {
			return PathPage{}, err
		}
		items = append(items, item)
	}
	totalPages := int((total + int64(query.PageSize) - 1) / int64(query.PageSize))
	return PathPage{Items: items, Page: query.Page, PageSize: query.PageSize, Total: total, TotalPages: totalPages}, nil
}

func (s *Service) Path(ctx context.Context, userID, studyID, pathID uint) (PathDetail, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return PathDetail{}, err
	}
	var evaluation saasstore.KlineInverseEvaluation
	if err := s.db.WithContext(ctx).Where("study_id = ? AND path_id = ? AND status = ?", study.ID, pathID, "completed").Order("id DESC").First(&evaluation).Error; err != nil {
		return PathDetail{}, ErrNotFound
	}
	var path saasstore.KlineInversePath
	if err := s.db.WithContext(ctx).First(&path, pathID).Error; err != nil {
		return PathDetail{}, ErrNotFound
	}
	summary, err := s.pathSummary(ctx, evaluation)
	if err != nil {
		return PathDetail{}, err
	}
	var reports []saasstore.PerformanceReport
	_ = s.db.WithContext(ctx).Where("backtest_result_id = ? AND status = ?", evaluation.BacktestResultID, "completed").Order("id DESC").Find(&reports).Error
	reportIDs := make([]uint, 0, len(reports))
	for _, report := range reports {
		reportIDs = append(reportIDs, report.ID)
	}
	return PathDetail{PathSummary: summary, WarmupLength: path.WarmupLength, EvaluationLength: path.EvaluationLength, Coordinates: json.RawMessage(path.Coordinates), OHLC: json.RawMessage(path.OHLC), Features: json.RawMessage(evaluation.Features), PerformanceReportIDs: reportIDs}, nil
}

func (s *Service) Lineage(ctx context.Context, userID, studyID, pathID uint) ([]LineageEdgeDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return nil, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("study_id = ? AND path_id = ?", study.ID, pathID).Count(&count).Error; err != nil || count == 0 {
		return nil, ErrNotFound
	}
	var rows []saasstore.KlineInverseLineageEdge
	if err := s.db.WithContext(ctx).Where("study_id = ? AND (child_path_id = ? OR parent_path_id = ?)", study.ID, pathID, pathID).Order("sequence_index ASC, parent_ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]LineageEdgeDescriptor, 0, len(rows))
	for _, row := range rows {
		channels := []string{}
		_ = json.Unmarshal(row.ChangedChannels, &channels)
		result = append(result, LineageEdgeDescriptor{ID: row.ID, ChildPathID: row.ChildPathID, ParentPathID: row.ParentPathID, RequestedOperation: row.RequestedOperation, ActualOperation: row.ActualOperation, ChangedStart: row.ChangedStart, ChangedLength: row.ChangedLength, ChangedChannels: channels, Amplitude: row.Amplitude, BatchID: row.BatchID})
	}
	return result, nil
}

func (s *Service) Boundary(ctx context.Context, userID, studyID, anchorPathID uint, batchIDs []uint) (BoundaryResponse, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return BoundaryResponse{}, err
	}
	var anchorEval saasstore.KlineInverseEvaluation
	if err := s.db.WithContext(ctx).Where("study_id = ? AND path_id = ? AND permanent = ? AND pass_a = ?", study.ID, anchorPathID, true, true).First(&anchorEval).Error; err != nil {
		return BoundaryResponse{}, ErrNotFound
	}
	anchorSummary, err := s.pathSummary(ctx, anchorEval)
	if err != nil {
		return BoundaryResponse{}, err
	}
	anchorCandidate, err := candidateFromEvaluation(ctx, s.db, anchorEval)
	if err != nil {
		return BoundaryResponse{}, err
	}
	query := s.db.WithContext(ctx).Where("study_id = ? AND parent_path_id = ?", study.ID, anchorPathID)
	if len(batchIDs) > 0 {
		query = query.Where("batch_id IN ?", batchIDs)
	}
	var edges []saasstore.KlineInverseLineageEdge
	if err := query.Order("sequence_index ASC").Find(&edges).Error; err != nil {
		return BoundaryResponse{}, err
	}
	points := make([]BoundaryPoint, 0, len(edges))
	for _, edge := range edges {
		var evaluation saasstore.KlineInverseEvaluation
		if err := s.db.WithContext(ctx).Where("study_id = ? AND path_id = ? AND status = ?", study.ID, edge.ChildPathID, "completed").First(&evaluation).Error; err != nil {
			continue
		}
		child, err := candidateFromEvaluation(ctx, s.db, evaluation)
		if err != nil {
			return BoundaryResponse{}, err
		}
		distance, err := core.PathDistance(anchorCandidate.Path, child.Path, canonical.FinalBounds)
		if err != nil {
			return BoundaryResponse{}, err
		}
		points = append(points, BoundaryPoint{ChildPathID: edge.ChildPathID, Operation: edge.ActualOperation, Distance: distance, QRelative: evaluation.QRelative, QAbsolute: evaluation.QAbsolute, State: evaluation.OutcomeState, ChangedStart: edge.ChangedStart, ChangedLength: edge.ChangedLength, Amplitude: edge.Amplitude, BatchID: edge.BatchID})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Distance.Total < points[j].Distance.Total })
	result := BoundaryResponse{Anchor: anchorSummary, Points: points}
	for _, point := range points {
		if anchorEval.PassA && point.QRelative <= 0 && (result.NearestFailureA == nil || point.Distance.Total < *result.NearestFailureA) {
			value := point.Distance.Total
			result.NearestFailureA = &value
		}
		if anchorEval.PassB && (point.QRelative <= 0 || point.QAbsolute <= 0) && (result.NearestFailureB == nil || point.Distance.Total < *result.NearestFailureB) {
			value := point.Distance.Total
			result.NearestFailureB = &value
		}
	}
	result.PassCurveA = passCurve(points, func(point BoundaryPoint) bool { return point.QRelative > 0 })
	result.PassCurveB = passCurve(points, func(point BoundaryPoint) bool { return point.QRelative > 0 && point.QAbsolute > 0 })
	return result, nil
}

func (s *Service) Comparison(ctx context.Context, userID, studyID, snapshotID uint) (ComparisonDescriptor, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return ComparisonDescriptor{}, err
	}
	snapshot, err := s.loadSnapshot(ctx, study, snapshotID)
	if err != nil {
		return ComparisonDescriptor{}, err
	}
	return ComparisonDescriptor{StudyID: study.ID, SnapshotID: snapshot.ID, SnapshotVersion: snapshot.SchemaVersion, ContentHash: snapshot.ContentHash, SourceKind: study.SourceKind, SourceID: canonical.SourceID, ParameterHash: study.ParameterHash, LazyBlocks: []string{"map", "features", "paths", "lineage", "anchor_boundary", "backtests", "performance_reports"}, ReadOnly: true}, nil
}

func (s *Service) loadSnapshot(ctx context.Context, study saasstore.KlineInverseStudy, snapshotID uint) (saasstore.KlineInverseArchiveSnapshot, error) {
	if snapshotID == 0 && study.CurrentSnapshotID != nil {
		snapshotID = *study.CurrentSnapshotID
	}
	if snapshotID == 0 {
		return saasstore.KlineInverseArchiveSnapshot{}, ErrNotFound
	}
	var snapshot saasstore.KlineInverseArchiveSnapshot
	if err := s.db.WithContext(ctx).Where("id = ? AND study_id = ?", snapshotID, study.ID).First(&snapshot).Error; err != nil {
		return snapshot, ErrNotFound
	}
	return snapshot, nil
}

func (s *Service) pathSummary(ctx context.Context, evaluation saasstore.KlineInverseEvaluation) (PathSummary, error) {
	var path saasstore.KlineInversePath
	if err := s.db.WithContext(ctx).First(&path, evaluation.PathID).Error; err != nil {
		return PathSummary{}, err
	}
	return PathSummary{ID: path.ID, PathHash: path.PathHash, EvaluationID: evaluation.ID, CellIndex: evaluation.CellIndex, OutcomeState: evaluation.OutcomeState, PassA: evaluation.PassA, PassB: evaluation.PassB, QRelative: evaluation.QRelative, QAbsolute: evaluation.QAbsolute, BacktestResultID: evaluation.BacktestResultID, PermanentReason: path.PermanentReason}, nil
}

func validFeatureName(value string) bool {
	for _, name := range core.FeatureNames {
		if name == value {
			return true
		}
	}
	return false
}

func passCurve(points []BoundaryPoint, pass func(BoundaryPoint) bool) []PassStep {
	result := make([]PassStep, 0, len(points))
	passed := 0
	for index, point := range points {
		if pass(point) {
			passed++
		}
		if index+1 < len(points) && math.Abs(points[index+1].Distance.Total-point.Distance.Total) < 1e-15 {
			continue
		}
		result = append(result, PassStep{Radius: point.Distance.Total, Passed: passed, Total: index + 1, Rate: float64(passed) / float64(index+1)})
	}
	return result
}
