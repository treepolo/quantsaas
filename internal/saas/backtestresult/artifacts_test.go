package backtestresult

import (
	"errors"
	"testing"

	"quantsaas/internal/backtestcore"
	saasstore "quantsaas/internal/saas/store"
)

func TestArtifactsBuildOrderedBlocksAndVerifyRecordGraph(t *testing.T) {
	identity, err := BuildIdentity(baseSpecInput(), testBars())
	if err != nil {
		t.Fatal(err)
	}
	coreResult, path := testResultPath(600)
	summary, err := BuildSummary(coreResult, 0.2, SummaryOptions{Extra: map[string]any{"metric_version": "legacy-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := BuildArtifacts(identity.SpecContentHash, summary, path, 256)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(artifacts.Blocks), 3; got != want {
		t.Fatalf("block count = %d, want %d", got, want)
	}
	if artifacts.Manifest.PointCount != 600 || artifacts.Manifest.Blocks[2].PointCount != 88 {
		t.Fatalf("unexpected manifest: %+v", artifacts.Manifest)
	}
	if summary.ExposureDaysRatio != 0.5 || summary.AverageActualExposure != 0.25 {
		t.Fatalf("unexpected exposure summary: ratio=%v average=%v", summary.ExposureDaysRatio, summary.AverageActualExposure)
	}

	spec := specModel(identity)
	spec.ID = 11
	activeKey := identity.BacktestKey + "|" + ResultSchemaVersion
	result := saasstore.BacktestResult{
		ID:               22,
		BacktestSpecID:   spec.ID,
		BacktestKey:      spec.BacktestKey,
		ResultVersion:    ResultSchemaVersion,
		Status:           saasstore.BacktestResultStatusCompleted,
		ActiveKey:        &activeKey,
		SummaryHash:      artifacts.SummaryHash,
		PathManifest:     saasstore.JSONB(artifacts.ManifestJSON),
		PathManifestHash: artifacts.ManifestHash,
		ContentHash:      artifacts.ResultContentHash,
		PathBlockCount:   artifacts.Manifest.BlockCount,
		PathPointCount:   artifacts.Manifest.PointCount,
		PathState:        saasstore.BacktestPathStateAvailable,
	}
	summaryModel := summaryModel(result.ID, artifacts)
	summaryModel.ID = 33
	blocks := make([]saasstore.BacktestPathBlock, 0, len(artifacts.Blocks))
	for index, artifact := range artifacts.Blocks {
		model := pathBlockModel(result.ID, artifact)
		model.ID = uint(40 + index)
		blocks = append(blocks, model)
	}

	report, err := VerifyRecords(spec, result, &summaryModel, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.PathVerified || report.PointCount != 600 {
		t.Fatalf("unexpected integrity report: %+v", report)
	}

	tampered, err := DecodePathBlock([]byte(blocks[0].Payload))
	if err != nil {
		t.Fatal(err)
	}
	tampered.Points[0].Price++
	tamperedJSON, err := canonicalJSON(tampered)
	if err != nil {
		t.Fatal(err)
	}
	blocks[0].Payload = saasstore.JSONB(tamperedJSON)
	if _, err := VerifyRecords(spec, result, &summaryModel, blocks); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered block error = %v, want integrity error", err)
	}
}

func TestDeletedPathKeepsVerifiableSummaryAndManifest(t *testing.T) {
	identity, err := BuildIdentity(baseSpecInput(), testBars())
	if err != nil {
		t.Fatal(err)
	}
	coreResult, path := testResultPath(10)
	summary, err := BuildSummary(coreResult, 0.1, SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := BuildArtifacts(identity.SpecContentHash, summary, path, 4)
	if err != nil {
		t.Fatal(err)
	}
	spec := specModel(identity)
	spec.ID = 1
	result := saasstore.BacktestResult{
		ID: 2, BacktestSpecID: 1, BacktestKey: spec.BacktestKey,
		ResultVersion: ResultSchemaVersion, Status: saasstore.BacktestResultStatusArchived,
		SummaryHash: artifacts.SummaryHash, PathManifest: saasstore.JSONB(artifacts.ManifestJSON),
		PathManifestHash: artifacts.ManifestHash, ContentHash: artifacts.ResultContentHash,
		PathBlockCount: artifacts.Manifest.BlockCount, PathPointCount: artifacts.Manifest.PointCount,
		PathState: saasstore.BacktestPathStateDeleted,
	}
	summaryModel := summaryModel(result.ID, artifacts)
	report, err := VerifyRecords(spec, result, &summaryModel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.SummaryOnly || report.PathVerified {
		t.Fatalf("unexpected summary-only report: %+v", report)
	}
}

func testResultPath(count int) (backtestcore.Result, []PathPoint) {
	points := make([]backtestcore.NAVPoint, 0, count)
	path := make([]PathPoint, 0, count)
	for index := 0; index < count; index++ {
		exposure := 0.0
		if index%2 == 1 {
			exposure = 0.5
		}
		point := backtestcore.NAVPoint{
			TimeMs:               int64(index+1) * 86_400_000,
			Price:                100 + float64(index),
			TotalEquity:          1000 + float64(index%7-3),
			Cash:                 1000 * (1 - exposure),
			AssetQuantity:        exposure * 10,
			ActualExposureWeight: exposure,
			DailyReturn:          0.001,
		}
		points = append(points, point)
		benchmark := 1000 + float64(index)
		path = append(path, PathPoint{NAVPoint: point, BenchmarkEquity: &benchmark})
	}
	return backtestcore.Result{
		Path: points, FinalAssets: points[len(points)-1].TotalEquity,
		TotalReturn: 0.12, TradeCount: 7, TotalInjected: 1000,
		EvaluationInitial: 1000, EvaluationStartMs: points[0].TimeMs,
		EvaluationEndMs: points[len(points)-1].TimeMs,
	}, path
}
