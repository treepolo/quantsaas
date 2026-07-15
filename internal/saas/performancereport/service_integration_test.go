package performancereport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPerformanceReportPersistsReusesAndLinksNoCashFlowResult(t *testing.T) {
	db := openPerformanceIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p04@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	otherUser := saasstore.User{Email: "p04-other@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	seedPerformanceMarket(t, db)

	paramsRaw, err := json.Marshal(sigmoiddca.DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	gene := saasstore.GeneRecord{
		StrategyID: sigmoiddca.StrategyID, InstrumentID: "BTCUSDT", DataSource: "binance", Interval: "1d",
		ExecutionMode: "close_same_bar", Role: saasstore.GeneRoleChallenger,
		Tags: saasstore.JSONB(`[]`), SearchConfig: saasstore.JSONB(`{}`), ParamPack: saasstore.JSONB(paramsRaw), WindowScore: saasstore.JSONB(`{}`),
	}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}
	monthly := 100.0
	backtestService := backtest.NewService(db)
	run, err := backtestService.Create(ctx, user.ID, backtest.CreateRequest{
		StrategyID: sigmoiddca.StrategyID, InstrumentID: "BTCUSDT", DataSource: "binance", Symbol: "BTCUSDT",
		Interval: "1d", ExecutionMode: "close_same_bar", Source: backtest.SourceCandidate, CandidateID: gene.ID, MonthlyDCA: &monthly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.BacktestResultID == 0 {
		t.Fatal("missing standardized source result")
	}

	service := NewService(db)
	before, err := service.LatestForGenome(ctx, user.ID, gene.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.BacktestResultID == nil || *before.BacktestResultID != run.BacktestResultID || before.Report != nil {
		t.Fatalf("unexpected pre-analysis genome summary: %+v", before)
	}
	created, err := service.Create(ctx, user.ID, run.BacktestResultID, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.ContentHash == "" || created.Summary == nil || created.Reused {
		t.Fatalf("invalid created report: %+v", created)
	}
	if created.AnnualizationBacktestResultID == created.BacktestResultID {
		t.Fatal("cash-flow source did not create a separate no-cash-flow result")
	}
	if created.Summary.Relative.StrategyNoCashFlowAnnualized == nil || !created.Summary.Relative.AnnualizationUsesNoCashFlowResult {
		t.Fatalf("missing no-cash-flow annualization: %+v", created.Summary.Relative)
	}
	annualizationResult, err := backtestresult.NewStore(db).Load(ctx, created.AnnualizationBacktestResultID, false)
	if err != nil {
		t.Fatal(err)
	}
	annualizationIdentity, err := backtestresult.DecodeIdentity(annualizationResult.Spec.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if annualizationIdentity.Snapshot.MonthlyContribution != 0 {
		t.Fatalf("annualization monthly contribution = %v, want 0", annualizationIdentity.Snapshot.MonthlyContribution)
	}

	reused, err := service.Create(ctx, user.ID, run.BacktestResultID, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != created.ID || !reused.Reused {
		t.Fatalf("report reuse failed: first=%d second=%d reused=%v", created.ID, reused.ID, reused.Reused)
	}
	listed, err := service.ListForResult(ctx, user.ID, run.BacktestResultID)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("report list = %+v, err=%v", listed, err)
	}
	chart, err := service.GetChart(ctx, user.ID, created.ID, "return_accumulation")
	if err != nil {
		t.Fatal(err)
	}
	if chart.ContentHash == "" || chart.PointCount == 0 || len(chart.Data) == 0 {
		t.Fatalf("invalid lazy chart response: %+v", chart)
	}
	integrity, err := service.Verify(ctx, user.ID, created.ID)
	if err != nil || !integrity.Valid || !integrity.ChartsVerified {
		t.Fatalf("report integrity = %+v, err=%v", integrity, err)
	}
	after, err := service.LatestForGenome(ctx, user.ID, gene.ID)
	if err != nil || after.Report == nil || after.Report.ID != created.ID {
		t.Fatalf("post-analysis genome summary = %+v, err=%v", after, err)
	}
	if _, err := service.Get(ctx, otherUser.ID, created.ID); !errors.Is(err, ErrAccessNotFound) {
		t.Fatalf("unauthorized report read error = %v", err)
	}
	otherRun, err := backtestService.Create(ctx, otherUser.ID, backtest.CreateRequest{
		StrategyID: sigmoiddca.StrategyID, InstrumentID: "BTCUSDT", DataSource: "binance", Symbol: "BTCUSDT",
		Interval: "1d", ExecutionMode: "close_same_bar", Source: backtest.SourceCandidate, CandidateID: gene.ID, MonthlyDCA: &monthly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if otherRun.BacktestResultID != run.BacktestResultID || !otherRun.ReusedResult {
		t.Fatalf("cross-user standardized result was not reused: %+v", otherRun)
	}
	otherDescriptor, err := service.Get(ctx, otherUser.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherDescriptor.SourceResult.BacktestRunIDs) != 1 || otherDescriptor.SourceResult.BacktestRunIDs[0] != otherRun.ID {
		t.Fatalf("cross-user run IDs leaked: %+v", otherDescriptor.SourceResult.BacktestRunIDs)
	}
	if len(created.SourceResult.BacktestRunIDs) != 1 || created.SourceResult.BacktestRunIDs[0] != run.ID {
		t.Fatalf("owner run IDs = %+v", created.SourceResult.BacktestRunIDs)
	}

	standardDescriptor, err := backtestService.GetStandardResult(ctx, user.ID, run.BacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(standardDescriptor.PerformanceReports) != 1 || standardDescriptor.PerformanceReports[0].ID != created.ID {
		t.Fatalf("backtest/report reverse link = %+v", standardDescriptor.PerformanceReports)
	}
	references, err := backtestresult.NewStore(db).References(ctx, run.BacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(references.PerformanceReportIDs) != 1 || references.PerformanceReportIDs[0] != created.ID {
		t.Fatalf("source result references = %+v", references)
	}
	annualReferences, err := backtestresult.NewStore(db).References(ctx, created.AnnualizationBacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(annualReferences.AnnualizationPerformanceReportIDs) != 1 || annualReferences.AnnualizationPerformanceReportIDs[0] != created.ID {
		t.Fatalf("annualization result references = %+v", annualReferences)
	}
	if _, err := backtestresult.NewStore(db).DeletePathDetail(ctx, run.BacktestResultID, false); !errors.Is(err, backtestresult.ErrResultReferenced) {
		t.Fatalf("report reference did not protect source path: %v", err)
	}

	betaReport, err := service.Create(ctx, user.ID, run.BacktestResultID, CreateRequest{RiskFreeAnnualRate: 0.02, BetaBenchmarkInstrument: "SP500"})
	if err != nil {
		t.Fatal(err)
	}
	if betaReport.ID == created.ID || betaReport.Summary == nil || betaReport.Summary.Beta.Value == nil || betaReport.Settings.BetaBenchmark == nil {
		t.Fatalf("invalid Beta report: %+v", betaReport)
	}

	testConcurrentReportReservation(t, ctx, service, created)
	testCorruptReportIsInvalidated(t, ctx, service, created)
}

func testConcurrentReportReservation(t *testing.T, ctx context.Context, service *Service, descriptor *Descriptor) {
	t.Helper()
	settings := descriptor.Settings
	settings.RiskFreeAnnualRate = 0.12345
	identity, err := BuildIdentity(IdentitySnapshot{
		BacktestResultID: descriptor.BacktestResultID, BacktestResultVersion: descriptor.SourceResult.ResultVersion,
		BacktestResultContentHash:          descriptor.SourceResult.ContentHash,
		AnnualizationBacktestResultID:      descriptor.AnnualizationBacktestResultID,
		AnnualizationBacktestResultVersion: descriptor.SourceResult.ResultVersion,
		AnnualizationResultContentHash:     loadResultHash(t, service.db, descriptor.AnnualizationBacktestResultID), Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	reservations := make(chan Reservation, workers)
	errorsByWorker := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			reservation, err := service.reports.Reserve(ctx, identity)
			if err != nil {
				errorsByWorker <- err
				return
			}
			reservations <- reservation
		}()
	}
	group.Wait()
	close(reservations)
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Fatal(err)
	}
	createdCount := 0
	reportID := uint(0)
	for reservation := range reservations {
		if reservation.Created {
			createdCount++
		}
		if reportID == 0 {
			reportID = reservation.Report.ID
		} else if reportID != reservation.Report.ID {
			t.Fatal("concurrent reservation returned different IDs")
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent created count = %d, want 1", createdCount)
	}
	if err := service.reports.Cancel(ctx, reportID, "test cleanup"); err != nil {
		t.Fatal(err)
	}
}

func testCorruptReportIsInvalidated(t *testing.T, ctx context.Context, service *Service, descriptor *Descriptor) {
	t.Helper()
	var block saasstore.PerformanceReportChartBlock
	if err := service.db.Where("performance_report_id = ?", descriptor.ID).Order("id ASC").First(&block).Error; err != nil {
		t.Fatal(err)
	}
	block.Payload = saasstore.JSONB(`{"tampered":true}`)
	if err := service.db.Model(&saasstore.PerformanceReportChartBlock{}).Where("id = ?", block.ID).Update("payload", block.Payload).Error; err != nil {
		t.Fatal(err)
	}
	annualHash := loadResultHash(t, service.db, descriptor.AnnualizationBacktestResultID)
	identity, err := BuildIdentity(IdentitySnapshot{
		BacktestResultID: descriptor.BacktestResultID, BacktestResultVersion: descriptor.SourceResult.ResultVersion,
		BacktestResultContentHash:          descriptor.SourceResult.ContentHash,
		AnnualizationBacktestResultID:      descriptor.AnnualizationBacktestResultID,
		AnnualizationBacktestResultVersion: descriptor.SourceResult.ResultVersion,
		AnnualizationResultContentHash:     annualHash, Settings: descriptor.Settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := service.reports.Reserve(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !replacement.Created || replacement.Report.ID == descriptor.ID {
		t.Fatalf("corrupt report was reused: %+v", replacement)
	}
	var corrupt saasstore.PerformanceReport
	if err := service.db.First(&corrupt, descriptor.ID).Error; err != nil {
		t.Fatal(err)
	}
	if corrupt.Status != saasstore.PerformanceReportStatusInvalidated || corrupt.ActiveKey != nil {
		t.Fatalf("corrupt lifecycle = %+v", corrupt)
	}
	if err := service.reports.Cancel(ctx, replacement.Report.ID, "test cleanup"); err != nil {
		t.Fatal(err)
	}
}

func loadResultHash(t *testing.T, db *gorm.DB, id uint) string {
	t.Helper()
	var result saasstore.BacktestResult
	if err := db.First(&result, id).Error; err != nil {
		t.Fatal(err)
	}
	return result.ContentHash
}

func seedPerformanceMarket(t *testing.T, db *gorm.DB) {
	t.Helper()
	intervals := saasstore.JSONB(`["1d"]`)
	instruments := []saasstore.ResearchInstrument{
		{ID: "BTCUSDT", Symbol: "BTCUSDT", DisplayName: "Bitcoin", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", SortOrder: 1, Enabled: true},
		{ID: "SP500", Symbol: "^GSPC", DisplayName: "S&P 500", DataSource: "yahoo", SupportedIntervals: intervals, Market: "us", SortOrder: 2, Enabled: true},
	}
	if err := db.Create(&instruments).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	rows := make([]saasstore.KLine, 0, 900)
	for index := 0; index < 450; index++ {
		timestamp := start + int64(index)*86_400_000
		btc := 100 + float64(index)*0.08 + 4*math.Sin(float64(index)/11)
		sp := 200 + float64(index)*0.05 + 2*math.Sin(float64(index)/13)
		rows = append(rows,
			saasstore.KLine{InstrumentID: "BTCUSDT", Source: "binance", Symbol: "BTCUSDT", Interval: "1d", OpenTime: timestamp, Open: btc, High: btc * 1.02, Low: btc * 0.98, Close: btc * 1.003, Volume: 1000 + float64(index)},
			saasstore.KLine{InstrumentID: "SP500", Source: "yahoo", Symbol: "^GSPC", Interval: "1d", OpenTime: timestamp, Open: sp, High: sp * 1.01, Low: sp * 0.99, Close: sp * 1.002, Volume: 2000 + float64(index)},
		)
	}
	if err := db.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}
}

func openPerformanceIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p04_performance_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scoped, err := performanceDSNWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performanceDSNWithSearchPath(dsn string, schema string) (string, error) {
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema, nil
}
