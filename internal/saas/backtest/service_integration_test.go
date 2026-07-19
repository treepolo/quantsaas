package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/saas/backtestresult"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCreatePersistsAndReusesStandardizedResult(t *testing.T) {
	db := openBacktestIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p03@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, err := saasstore.NewJSONB([]string{"1d"})
	if err != nil {
		t.Fatal(err)
	}
	instrument := saasstore.ResearchInstrument{
		ID: "BTCUSDT", Symbol: "BTCUSDT", DisplayName: "Bitcoin",
		DataSource: "binance", SupportedIntervals: intervals, Market: "crypto",
		SortOrder: 1, Enabled: true,
	}
	if err := db.Create(&instrument).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_700_000_000_000)
	rows := make([]saasstore.KLine, 0, 220)
	for index := 0; index < 220; index++ {
		price := 100 + float64(index%17) + float64(index)/20
		rows = append(rows, saasstore.KLine{
			InstrumentID: "BTCUSDT", Source: "binance", Symbol: "BTCUSDT", Interval: "1d",
			OpenTime: start + int64(index)*86_400_000,
			Open:     price, High: price * 1.02, Low: price * 0.98, Close: price * 1.005, Volume: 1000 + float64(index),
		})
	}
	if err := db.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	var mainRuns atomic.Int32
	service.runSigmoidDCA = func(request backtestcore.SigmoidDCARequest) (backtestcore.Result, error) {
		mainRuns.Add(1)
		return backtestcore.RunSigmoidDCA(request)
	}
	request := CreateRequest{
		StrategyID:    "sigmoid-dca-btc",
		InstrumentID:  "BTCUSDT",
		DataSource:    "binance",
		ExecutionMode: "close_same_bar",
		Symbol:        "BTCUSDT",
		Interval:      "1d",
		Source:        SourceCustom,
		CustomParams:  json.RawMessage(`{"rebalance_threshold":0.75}`),
	}

	first, err := service.Create(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("backtest run IDs should remain distinct task records")
	}
	if first.BacktestResultID == 0 || first.BacktestResultID != second.BacktestResultID {
		t.Fatalf("standard result IDs = %d and %d, want the same non-zero ID", first.BacktestResultID, second.BacktestResultID)
	}
	if first.ReusedResult || !second.ReusedResult {
		t.Fatalf("reused flags = %v, %v", first.ReusedResult, second.ReusedResult)
	}
	if got := mainRuns.Load(); got != 1 {
		t.Fatalf("main path runner calls = %d, want 1", got)
	}
	if first.ResultContentHash == "" || first.ResultContentHash != second.ResultContentHash {
		t.Fatalf("result hashes = %q and %q", first.ResultContentHash, second.ResultContentHash)
	}
	if len(first.NAV) == 0 || len(first.NAV) != len(second.NAV) {
		t.Fatalf("NAV lengths = %d and %d", len(first.NAV), len(second.NAV))
	}
	for index, point := range second.NAV {
		if point.ActualExposureWeight < 0 || point.ActualExposureWeight > 1 {
			t.Fatalf("NAV point %d actual exposure = %v", index, point.ActualExposureWeight)
		}
	}

	loadedFirst, err := service.Get(ctx, user.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedFirst.BacktestResultID != first.BacktestResultID || loadedFirst.BacktestKey != first.BacktestKey {
		t.Fatalf("run/result trace lost: %+v", loadedFirst)
	}
	descriptor, err := service.GetStandardResult(ctx, user.ID, first.BacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.BacktestRunIDs) != 2 || descriptor.BacktestRunIDs[0] != first.ID || descriptor.BacktestRunIDs[1] != second.ID {
		t.Fatalf("result/run reverse trace = %v", descriptor.BacktestRunIDs)
	}
	block, err := service.GetStandardPathBlock(ctx, user.ID, first.BacktestResultID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Block.Points) == 0 || block.ContentHash == "" {
		t.Fatalf("invalid path block response: %+v", block)
	}
	report, err := service.VerifyStandardResult(ctx, user.ID, first.BacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.PathVerified {
		t.Fatalf("integrity report = %+v", report)
	}

	var specCount, resultCount, summaryCount, blockCount int64
	for model, count := range map[any]*int64{
		&saasstore.BacktestSpec{}:          &specCount,
		&saasstore.BacktestResult{}:        &resultCount,
		&saasstore.BacktestResultSummary{}: &summaryCount,
		&saasstore.BacktestPathBlock{}:     &blockCount,
	} {
		if err := db.Model(model).Count(count).Error; err != nil {
			t.Fatal(err)
		}
	}
	if specCount != 1 || resultCount != 1 || summaryCount != 1 || blockCount == 0 {
		t.Fatalf("stored counts spec=%d result=%d summary=%d blocks=%d", specCount, resultCount, summaryCount, blockCount)
	}
	if references, err := service.results.DeletePathDetail(ctx, first.BacktestResultID, false); !errors.Is(err, backtestresult.ErrResultReferenced) || len(references.BacktestRunIDs) != 2 {
		t.Fatalf("reference protection = %+v, err=%v", references, err)
	}

	fee := 0.0123
	concurrentRequest := request
	concurrentRequest.FeeRate = &fee
	prepared, err := service.prepare(ctx, user.ID, concurrentRequest)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	reservations := make(chan backtestresult.Reservation, workers)
	errorsByWorker := make(chan error, workers)
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			reservation, reserveErr := service.results.Reserve(ctx, prepared.identity)
			if reserveErr != nil {
				errorsByWorker <- reserveErr
				return
			}
			reservations <- reservation
		}()
	}
	waitGroup.Wait()
	close(reservations)
	close(errorsByWorker)
	for reserveErr := range errorsByWorker {
		t.Fatal(reserveErr)
	}
	createdCount := 0
	concurrentResultID := uint(0)
	for reservation := range reservations {
		if reservation.Created {
			createdCount++
		}
		if concurrentResultID == 0 {
			concurrentResultID = reservation.Result.ID
		} else if reservation.Result.ID != concurrentResultID {
			t.Fatalf("concurrent reservations returned result IDs %d and %d", concurrentResultID, reservation.Result.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent created reservations = %d, want 1", createdCount)
	}
	if err := service.results.Cancel(ctx, concurrentResultID, "integration test cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.results.DeleteFailed(ctx, concurrentResultID); err != nil {
		t.Fatal(err)
	}

	corruptFee := 0.0021
	corruptRequest := request
	corruptRequest.FeeRate = &corruptFee
	corruptRun, err := service.Create(ctx, user.ID, corruptRequest)
	if err != nil {
		t.Fatal(err)
	}
	corruptPrepared, err := service.prepare(ctx, user.ID, service.normalizeRequest(ctx, corruptRequest))
	if err != nil {
		t.Fatal(err)
	}
	var corruptBlock saasstore.BacktestPathBlock
	if err := db.Where("backtest_result_id = ?", corruptRun.BacktestResultID).Order("block_index ASC").First(&corruptBlock).Error; err != nil {
		t.Fatal(err)
	}
	blockPayload, err := backtestresult.DecodePathBlock([]byte(corruptBlock.Payload))
	if err != nil {
		t.Fatal(err)
	}
	blockPayload.Points[0].Price++
	tamperedPayload, err := json.Marshal(blockPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saasstore.BacktestPathBlock{}).Where("id = ?", corruptBlock.ID).
		Update("payload", saasstore.JSONB(tamperedPayload)).Error; err != nil {
		t.Fatal(err)
	}
	replacementForCorrupt, err := service.results.Reserve(ctx, corruptPrepared.identity)
	if err != nil {
		t.Fatal(err)
	}
	if !replacementForCorrupt.Created || replacementForCorrupt.Result.ID == corruptRun.BacktestResultID {
		t.Fatalf("corrupt completed result was reused: %+v", replacementForCorrupt)
	}
	var corruptStored saasstore.BacktestResult
	if err := db.First(&corruptStored, corruptRun.BacktestResultID).Error; err != nil {
		t.Fatal(err)
	}
	if corruptStored.Status != saasstore.BacktestResultStatusInvalidated || corruptStored.ActiveKey != nil {
		t.Fatalf("corrupt result lifecycle = %+v", corruptStored)
	}
	if err := service.results.Cancel(ctx, replacementForCorrupt.Result.ID, "integration test cleanup"); err != nil {
		t.Fatal(err)
	}

	if err := service.results.Invalidate(ctx, first.BacktestResultID, "dataset superseded"); err != nil {
		t.Fatal(err)
	}
	invalidatedRun, err := service.Get(ctx, user.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if invalidatedRun.ResultStatus != saasstore.BacktestResultStatusInvalidated || len(invalidatedRun.NAV) == 0 {
		t.Fatalf("invalidated historical result was not traceable: %+v", invalidatedRun)
	}
	if err := service.results.Archive(ctx, first.BacktestResultID); err != nil {
		t.Fatal(err)
	}
	originalPrepared, err := service.prepare(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := service.results.Reserve(ctx, originalPrepared.identity)
	if err != nil {
		t.Fatal(err)
	}
	if !replacement.Created || replacement.Result.ID == first.BacktestResultID {
		t.Fatalf("archived result was incorrectly reused: %+v", replacement)
	}
	if err := service.results.Cancel(ctx, replacement.Result.ID, "integration test cleanup"); err != nil {
		t.Fatal(err)
	}
	if references, err := service.results.DeletePathDetail(ctx, first.BacktestResultID, true); err != nil || len(references.BacktestRunIDs) != 2 {
		t.Fatalf("explicit path cleanup = %+v, err=%v", references, err)
	}
	summaryOnly, err := service.results.VerifyResult(ctx, first.BacktestResultID)
	if err != nil {
		t.Fatal(err)
	}
	if !summaryOnly.Valid || !summaryOnly.SummaryOnly || summaryOnly.PathVerified {
		t.Fatalf("summary-only integrity report = %+v", summaryOnly)
	}

	initial := 10000.0
	monthly := 0.0
	baselineRequest := request
	baselineRequest.Source = SourceBaseline
	baselineRequest.CustomParams = nil
	baselineRequest.InitialCapital = &initial
	baselineRequest.MonthlyDCA = &monthly
	baselineRun, err := service.Create(ctx, user.ID, baselineRequest)
	if err != nil {
		t.Fatal(err)
	}
	if baselineRun.Source != SourceBaseline || baselineRun.PositionStructure != "market_baseline" {
		t.Fatalf("baseline identity = source %q, position %q", baselineRun.Source, baselineRun.PositionStructure)
	}
	if baselineRun.LongTermFilterEnabled || len(baselineRun.NAV) == 0 {
		t.Fatalf("baseline result filter=%v nav=%d", baselineRun.LongTermFilterEnabled, len(baselineRun.NAV))
	}
	for index, point := range baselineRun.NAV {
		if point.ActualExposureWeight < 0.999999 {
			t.Fatalf("baseline NAV %d exposure = %.8f", index, point.ActualExposureWeight)
		}
	}
}

func openBacktestIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p03_backtest_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scopedDSN, err := withSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{})
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

func withSearchPath(dsn string, schema string) (string, error) {
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
