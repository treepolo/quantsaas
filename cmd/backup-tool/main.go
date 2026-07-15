package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/performancereport"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const backupVersion = 5

type incrementalBackup struct {
	Version                  int                                      `json:"version"`
	Kind                     string                                   `json:"kind"`
	CreatedAt                string                                   `json:"created_at"`
	Since                    string                                   `json:"since"`
	ResearchInstruments      []saasstore.ResearchInstrument           `json:"research_instruments"`
	KLines                   []saasstore.KLine                        `json:"k_lines"`
	DatasetMetadata          []saasstore.DatasetMetadata              `json:"dataset_metadata"`
	DailySnapshots           []saasstore.DailyExecutionSnapshot       `json:"daily_execution_snapshots"`
	GeneRecords              []saasstore.GeneRecord                   `json:"gene_records"`
	GeneObservations         []saasstore.GeneObservation              `json:"gene_observations"`
	EvolutionTasks           []saasstore.EvolutionTask                `json:"evolution_tasks"`
	BacktestSpecs            []saasstore.BacktestSpec                 `json:"backtest_specs"`
	BacktestResults          []saasstore.BacktestResult               `json:"backtest_results"`
	BacktestSummaries        []saasstore.BacktestResultSummary        `json:"backtest_result_summaries"`
	BacktestPathBlocks       []saasstore.BacktestPathBlock            `json:"backtest_path_blocks"`
	BacktestRuns             []saasstore.BacktestRun                  `json:"backtest_runs"`
	PerformanceReports       []saasstore.PerformanceReport            `json:"performance_reports"`
	PerformanceSummaries     []saasstore.PerformanceReportSummary     `json:"performance_report_summaries"`
	PerformanceCharts        []saasstore.PerformanceReportChartBlock  `json:"performance_report_chart_blocks"`
	ComputeTasks             []saasstore.ComputeTask                  `json:"compute_tasks"`
	ComputeCacheEntries      []saasstore.ComputeCacheEntry            `json:"compute_cache_entries"`
	ComputeTaskItems         []saasstore.ComputeTaskItem              `json:"compute_task_items"`
	ComputeDependencies      []saasstore.ComputeTaskDependency        `json:"compute_task_dependencies"`
	MarketSeries             []saasstore.MarketSeries                 `json:"market_series"`
	MarketDataVersions       []saasstore.MarketDataVersion            `json:"market_data_versions"`
	MarketVersionBars        []saasstore.MarketDataVersionBar         `json:"market_data_version_bars"`
	MarketVersionSources     []saasstore.MarketDataVersionSource      `json:"market_data_version_sources"`
	RecompositionPlans       []saasstore.RecompositionPlan            `json:"recomposition_plans"`
	RecompositionSegments    []saasstore.RecompositionPlanSegment     `json:"recomposition_plan_segments"`
	RecompositionPreviewBars []saasstore.RecompositionPreviewBar      `json:"recomposition_preview_bars"`
	RecompositionGenerations []saasstore.RecompositionGeneration      `json:"recomposition_generations"`
	RecompositionInstances   []saasstore.RecompositionSegmentInstance `json:"recomposition_segment_instances"`
	RecompositionLineage     []saasstore.RecompositionBarLineage      `json:"recomposition_bar_lineage"`
	ResearchDatasets         []saasstore.ResearchDataset              `json:"research_datasets"`
	ResearchDatasetSeries    []saasstore.ResearchDatasetSeries        `json:"research_dataset_series"`
	Counts                   map[string]int                           `json:"counts"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "錯誤:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "export-incremental":
		return runExportIncremental(args[1:])
	case "import-incremental":
		return runImportIncremental(args[1:])
	case "verify-backtests":
		return runVerifyBacktests(args[1:])
	case "verify-compute-tasks":
		return runVerifyComputeTasks(args[1:])
	case "backtest-references", "archive-backtest", "invalidate-backtest", "delete-backtest-path", "delete-failed-backtest":
		return runBacktestMaintenance(args[0], args[1:])
	case "help", "-h", "--help":
		return usage()
	default:
		return fmt.Errorf("未知指令: %s", args[0])
	}
}

func runExportIncremental(args []string) error {
	fs := flag.NewFlagSet("export-incremental", flag.ContinueOnError)
	out := fs.String("out", "backups/work/incremental.json", "增量備份 JSON 輸出路徑")
	sinceValue := fs.String("since", "", "只匯出此時間之後的資料，例如 2026-06-22T00:00:00Z")
	dsn := fs.String("dsn", "", "PostgreSQL DSN，留空時讀 DATABASE_DSN 或 config")
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sinceValue) == "" {
		return fmt.Errorf("export-incremental 需要 --since")
	}
	since, err := parseTime(*sinceValue)
	if err != nil {
		return err
	}
	db, err := openDB(*dsn, *configPath)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}
	backup, err := buildIncrementalBackup(db, since)
	if err != nil {
		return err
	}
	if err := ensureParentDir(*out); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("已建立增量備份：%s\n", *out)
	for table, count := range backup.Counts {
		fmt.Printf("- %s: %d\n", table, count)
	}
	return nil
}

func runImportIncremental(args []string) error {
	fs := flag.NewFlagSet("import-incremental", flag.ContinueOnError)
	in := fs.String("in", "backups/work/incremental.json", "增量備份 JSON 路徑")
	dsn := fs.String("dsn", "", "PostgreSQL DSN，留空時讀 DATABASE_DSN 或 config")
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var backup incrementalBackup
	if err := json.Unmarshal(raw, &backup); err != nil {
		return err
	}
	if backup.Version < 1 || backup.Version > backupVersion || backup.Kind != "incremental" {
		return fmt.Errorf("不支援的增量備份格式: version=%d kind=%s", backup.Version, backup.Kind)
	}
	db, err := openDB(*dsn, *configPath)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		return fmt.Errorf("套用增量備份前同步 schema 失敗: %w", err)
	}
	if err := applyIncrementalBackup(db, backup); err != nil {
		return err
	}
	fmt.Printf("已套用增量備份：%s\n", *in)
	return nil
}

func buildIncrementalBackup(db *gorm.DB, since time.Time) (incrementalBackup, error) {
	backup := incrementalBackup{
		Version:   backupVersion,
		Kind:      "incremental",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Since:     since.UTC().Format(time.RFC3339),
		Counts:    map[string]int{},
	}
	if err := changedSince(db, since, &backup.ResearchInstruments); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.KLines); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.DatasetMetadata); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.DailySnapshots); err != nil {
		return backup, err
	}
	if err := changedSinceUnscoped(db, since, &backup.GeneRecords); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.GeneObservations); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.EvolutionTasks); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.BacktestSpecs); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.BacktestResults); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.BacktestSummaries); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.BacktestPathBlocks); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.BacktestRuns); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.PerformanceReports); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.PerformanceSummaries); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.PerformanceCharts); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.ComputeTasks); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.ComputeCacheEntries); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.ComputeTaskItems); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.ComputeDependencies); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.MarketSeries); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.MarketDataVersions); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.MarketVersionBars); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.MarketVersionSources); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.RecompositionPlans); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.RecompositionSegments); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.RecompositionPreviewBars); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.RecompositionGenerations); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.RecompositionInstances); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.RecompositionLineage); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.ResearchDatasets); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.ResearchDatasetSeries); err != nil {
		return backup, err
	}
	if err := hydrateMarketVersionClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateComputeClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydratePerformanceClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateBacktestClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydratePerformanceClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateBacktestClosure(db, &backup); err != nil {
		return backup, err
	}
	backup.Counts["research_instruments"] = len(backup.ResearchInstruments)
	backup.Counts["k_lines"] = len(backup.KLines)
	backup.Counts["dataset_metadata"] = len(backup.DatasetMetadata)
	backup.Counts["daily_execution_snapshots"] = len(backup.DailySnapshots)
	backup.Counts["gene_records"] = len(backup.GeneRecords)
	backup.Counts["gene_observations"] = len(backup.GeneObservations)
	backup.Counts["evolution_tasks"] = len(backup.EvolutionTasks)
	backup.Counts["backtest_specs"] = len(backup.BacktestSpecs)
	backup.Counts["backtest_results"] = len(backup.BacktestResults)
	backup.Counts["backtest_result_summaries"] = len(backup.BacktestSummaries)
	backup.Counts["backtest_path_blocks"] = len(backup.BacktestPathBlocks)
	backup.Counts["backtest_runs"] = len(backup.BacktestRuns)
	backup.Counts["performance_reports"] = len(backup.PerformanceReports)
	backup.Counts["performance_report_summaries"] = len(backup.PerformanceSummaries)
	backup.Counts["performance_report_chart_blocks"] = len(backup.PerformanceCharts)
	backup.Counts["compute_tasks"] = len(backup.ComputeTasks)
	backup.Counts["compute_cache_entries"] = len(backup.ComputeCacheEntries)
	backup.Counts["compute_task_items"] = len(backup.ComputeTaskItems)
	backup.Counts["compute_task_dependencies"] = len(backup.ComputeDependencies)
	backup.Counts["market_series"] = len(backup.MarketSeries)
	backup.Counts["market_data_versions"] = len(backup.MarketDataVersions)
	backup.Counts["market_data_version_bars"] = len(backup.MarketVersionBars)
	backup.Counts["market_data_version_sources"] = len(backup.MarketVersionSources)
	backup.Counts["recomposition_plans"] = len(backup.RecompositionPlans)
	backup.Counts["recomposition_plan_segments"] = len(backup.RecompositionSegments)
	backup.Counts["recomposition_preview_bars"] = len(backup.RecompositionPreviewBars)
	backup.Counts["recomposition_generations"] = len(backup.RecompositionGenerations)
	backup.Counts["recomposition_segment_instances"] = len(backup.RecompositionInstances)
	backup.Counts["recomposition_bar_lineage"] = len(backup.RecompositionLineage)
	backup.Counts["research_datasets"] = len(backup.ResearchDatasets)
	backup.Counts["research_dataset_series"] = len(backup.ResearchDatasetSeries)
	return backup, nil
}

func hydrateMarketVersionClosure(db *gorm.DB, backup *incrementalBackup) error {
	changed := len(backup.MarketSeries)+len(backup.MarketDataVersions)+len(backup.MarketVersionBars)+len(backup.MarketVersionSources)+
		len(backup.RecompositionPlans)+len(backup.RecompositionSegments)+len(backup.RecompositionPreviewBars)+
		len(backup.RecompositionGenerations)+len(backup.RecompositionInstances)+len(backup.RecompositionLineage) > 0
	if !changed {
		return nil
	}
	for _, target := range []any{
		&backup.MarketSeries, &backup.MarketDataVersions, &backup.MarketVersionBars, &backup.MarketVersionSources,
		&backup.RecompositionPlans, &backup.RecompositionSegments, &backup.RecompositionPreviewBars,
		&backup.RecompositionGenerations, &backup.RecompositionInstances, &backup.RecompositionLineage,
		&backup.ResearchDatasets, &backup.ResearchDatasetSeries,
	} {
		if err := db.Find(target).Error; err != nil {
			return err
		}
	}
	outputIDs := make([]string, 0)
	taskIDs := map[uint]struct{}{}
	for _, version := range backup.MarketDataVersions {
		if version.OutputInstrumentID != nil {
			outputIDs = append(outputIDs, *version.OutputInstrumentID)
		}
		if version.ComputeTaskID != nil {
			taskIDs[*version.ComputeTaskID] = struct{}{}
		}
	}
	for _, plan := range backup.RecompositionPlans {
		if plan.PreviewTaskID != nil {
			taskIDs[*plan.PreviewTaskID] = struct{}{}
		}
	}
	for _, generation := range backup.RecompositionGenerations {
		if generation.ComputeTaskID != nil {
			taskIDs[*generation.ComputeTaskID] = struct{}{}
		}
	}
	if len(outputIDs) > 0 {
		var instruments []saasstore.ResearchInstrument
		if err := db.Where("id IN ?", outputIDs).Find(&instruments).Error; err != nil {
			return err
		}
		backup.ResearchInstruments = mergeResearchInstruments(backup.ResearchInstruments, instruments)
		var klines []saasstore.KLine
		if err := db.Where("instrument_id IN ?", outputIDs).Find(&klines).Error; err != nil {
			return err
		}
		backup.KLines = mergeKLines(backup.KLines, klines)
	}
	if len(taskIDs) > 0 {
		var tasks []saasstore.ComputeTask
		if err := db.Where("id IN ?", uintKeys(taskIDs)).Find(&tasks).Error; err != nil {
			return err
		}
		backup.ComputeTasks = mergeComputeTasks(backup.ComputeTasks, tasks)
	}
	return nil
}

func mergeResearchInstruments(left, right []saasstore.ResearchInstrument) []saasstore.ResearchInstrument {
	byID := make(map[string]saasstore.ResearchInstrument, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.ResearchInstrument, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeKLines(left, right []saasstore.KLine) []saasstore.KLine {
	byID := make(map[uint]saasstore.KLine, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.KLine, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func applyIncrementalBackup(db *gorm.DB, backup incrementalBackup) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := saveAll(tx, backup.ResearchInstruments); err != nil {
			return err
		}
		if err := saveAll(tx, backup.KLines); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DatasetMetadata); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DailySnapshots); err != nil {
			return err
		}
		if err := saveAllUnscoped(tx, backup.GeneRecords); err != nil {
			return err
		}
		if err := saveAll(tx, backup.GeneObservations); err != nil {
			return err
		}
		if err := saveAll(tx, backup.EvolutionTasks); err != nil {
			return err
		}
		if err := saveAll(tx, backup.BacktestSpecs); err != nil {
			return err
		}
		if err := saveAll(tx, backup.BacktestResults); err != nil {
			return err
		}
		if err := saveAll(tx, backup.BacktestSummaries); err != nil {
			return err
		}
		if err := saveAll(tx, backup.BacktestPathBlocks); err != nil {
			return err
		}
		if err := saveAll(tx, backup.PerformanceReports); err != nil {
			return err
		}
		if err := saveAll(tx, backup.PerformanceSummaries); err != nil {
			return err
		}
		if err := saveAll(tx, backup.PerformanceCharts); err != nil {
			return err
		}
		sortComputeTasksForRestore(backup.ComputeTasks)
		if err := saveAll(tx, backup.ComputeTasks); err != nil {
			return err
		}
		if err := saveAll(tx, backup.ComputeCacheEntries); err != nil {
			return err
		}
		if err := saveAll(tx, backup.ComputeTaskItems); err != nil {
			return err
		}
		if err := saveAll(tx, backup.ComputeDependencies); err != nil {
			return err
		}
		if err := saveAll(tx, backup.MarketSeries); err != nil {
			return err
		}
		if err := saveAll(tx, backup.MarketDataVersions); err != nil {
			return err
		}
		if err := saveAll(tx, backup.MarketVersionBars); err != nil {
			return err
		}
		if err := saveAll(tx, backup.MarketVersionSources); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionPlans); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionSegments); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionPreviewBars); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionGenerations); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionInstances); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RecompositionLineage); err != nil {
			return err
		}
		if err := saveAll(tx, backup.ResearchDatasets); err != nil {
			return err
		}
		if err := saveAll(tx, backup.ResearchDatasetSeries); err != nil {
			return err
		}
		if err := saveAll(tx, backup.BacktestRuns); err != nil {
			return err
		}
		if err := verifyRestoredBacktests(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredPerformanceReports(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredComputeTasks(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredMarketVersions(tx, backup); err != nil {
			return err
		}
		return resetSequences(tx)
	})
}

func verifyRestoredMarketVersions(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.MarketDataVersions {
		if saved.ArtifactKind != marketversion.ArtifactKindSegmentRecomposition || saved.Status != marketversion.VersionStatusCompleted {
			continue
		}
		var barRows []saasstore.MarketDataVersionBar
		if err := db.Where("version_id = ?", saved.ID).Order("ordinal ASC").Find(&barRows).Error; err != nil {
			return err
		}
		var lineageRows []saasstore.RecompositionBarLineage
		if err := db.Where("version_id = ?", saved.ID).Order("output_ordinal ASC").Find(&lineageRows).Error; err != nil {
			return err
		}
		if len(barRows) != saved.BarCount || len(lineageRows) != saved.BarCount {
			return fmt.Errorf("還原後行情版本 %d 的 K 線或血統數量不符", saved.ID)
		}
		bars := make([]marketversion.Bar, 0, len(barRows))
		lineage := make([]marketversion.BarLineage, 0, len(lineageRows))
		for index, row := range barRows {
			bars = append(bars, marketversion.Bar{Ordinal: row.Ordinal, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
			origin := lineageRows[index]
			lineage = append(lineage, marketversion.BarLineage{OutputOrdinal: origin.OutputOrdinal, OutputOpenTime: origin.OutputOpenTime, SegmentInstanceID: origin.SegmentInstanceKey, SourceVersionID: origin.SourceVersionID, SourceContentHash: origin.SourceContentHash, SourceOrdinal: origin.SourceOrdinal, SourceOpenTime: origin.SourceOpenTime})
		}
		hash, err := marketversion.HashRecompositionContent(bars, lineage)
		if err != nil || hash != saved.ContentHash {
			return fmt.Errorf("還原後行情版本 %d 內容雜湊不符", saved.ID)
		}
	}
	return nil
}

func runVerifyBacktests(args []string) error {
	fs := flag.NewFlagSet("verify-backtests", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "PostgreSQL DSN，留空時讀 DATABASE_DSN 或 config")
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := openDB(*dsn, *configPath)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		return fmt.Errorf("驗證前同步 schema 失敗: %w", err)
	}
	var ids []uint
	if err := db.Model(&saasstore.BacktestResult{}).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return err
	}
	resultStore := backtestresult.NewStore(db)
	for _, id := range ids {
		report, err := resultStore.VerifyResult(context.Background(), id)
		if err != nil {
			return fmt.Errorf("標準化回測結果 %d 驗證失敗: %w", id, err)
		}
		if !report.Valid {
			return fmt.Errorf("標準化回測結果 %d 驗證未通過", id)
		}
	}
	fmt.Printf("標準化回測結果完整性驗證通過：%d 筆\n", len(ids))
	var reportIDs []uint
	if err := db.Model(&saasstore.PerformanceReport{}).Order("id ASC").Pluck("id", &reportIDs).Error; err != nil {
		return err
	}
	reportStore := performancereport.NewStore(db)
	for _, id := range reportIDs {
		report, err := reportStore.Verify(context.Background(), id, true)
		if err != nil {
			return fmt.Errorf("報酬分析報告 %d 驗證失敗: %w", id, err)
		}
		if !report.Valid {
			return fmt.Errorf("報酬分析報告 %d 驗證未通過", id)
		}
	}
	fmt.Printf("報酬分析報告完整性驗證通過：%d 筆\n", len(reportIDs))
	return nil
}

func runVerifyComputeTasks(args []string) error {
	fs := flag.NewFlagSet("verify-compute-tasks", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "PostgreSQL DSN，留空時讀 DATABASE_DSN 或 config")
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := openDB(*dsn, *configPath)
	if err != nil {
		return err
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}
	backup := incrementalBackup{}
	if err := db.Order("id ASC").Find(&backup.ComputeTasks).Error; err != nil {
		return err
	}
	if err := db.Order("id ASC").Find(&backup.ComputeCacheEntries).Error; err != nil {
		return err
	}
	if err := db.Order("id ASC").Find(&backup.ComputeTaskItems).Error; err != nil {
		return err
	}
	if err := db.Order("id ASC").Find(&backup.ComputeDependencies).Error; err != nil {
		return err
	}
	if err := verifyRestoredComputeTasks(db, backup); err != nil {
		return err
	}
	fmt.Printf("計算任務驗證完成：tasks=%d items=%d caches=%d dependencies=%d\n", len(backup.ComputeTasks), len(backup.ComputeTaskItems), len(backup.ComputeCacheEntries), len(backup.ComputeDependencies))
	return nil
}

func runBacktestMaintenance(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	resultID := fs.Uint("id", 0, "標準化回測結果 ID")
	reason := fs.String("reason", "", "失效原因（invalidate-backtest 必填）")
	allowReferenced := fs.Bool("allow-referenced", false, "確認即使仍被引用也刪除 path detail")
	dsn := fs.String("dsn", "", "PostgreSQL DSN，留空時讀 DATABASE_DSN 或 config")
	configPath := fs.String("config", "config.yaml", "設定檔路徑")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resultID == 0 {
		return fmt.Errorf("%s 需要 --id", action)
	}
	if action == "invalidate-backtest" && strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("invalidate-backtest 需要 --reason")
	}
	db, err := openDB(*dsn, *configPath)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err == nil {
		defer sqlDB.Close()
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		return fmt.Errorf("維護前同步 schema 失敗: %w", err)
	}
	resultStore := backtestresult.NewStore(db)
	ctx := context.Background()
	references, err := resultStore.References(ctx, *resultID)
	if err != nil {
		return err
	}
	fmt.Printf("標準化回測結果 %d 的 BacktestRun 引用：%v\n", *resultID, references.BacktestRunIDs)
	fmt.Printf("標準化回測結果 %d 的 PerformanceReport 來源引用：%v\n", *resultID, references.PerformanceReportIDs)
	fmt.Printf("標準化回測結果 %d 的無現金流年化引用：%v\n", *resultID, references.AnnualizationPerformanceReportIDs)

	switch action {
	case "backtest-references":
		return nil
	case "archive-backtest":
		if err := resultStore.Archive(ctx, *resultID); err != nil {
			return err
		}
		fmt.Printf("已封存標準化回測結果：%d\n", *resultID)
	case "invalidate-backtest":
		if err := resultStore.Invalidate(ctx, *resultID, *reason); err != nil {
			return err
		}
		fmt.Printf("已標記標準化回測結果失效：%d\n", *resultID)
	case "delete-backtest-path":
		if _, err := resultStore.DeletePathDetail(ctx, *resultID, *allowReferenced); err != nil {
			return err
		}
		fmt.Printf("已刪除標準化回測 path detail，摘要仍保留：%d\n", *resultID)
	case "delete-failed-backtest":
		if _, err := resultStore.DeleteFailed(ctx, *resultID); err != nil {
			return err
		}
		fmt.Printf("已刪除未被引用的失敗／取消結果：%d\n", *resultID)
	default:
		return fmt.Errorf("未知維護動作: %s", action)
	}
	return nil
}

func hydrateBacktestClosure(db *gorm.DB, backup *incrementalBackup) error {
	resultIDs := map[uint]struct{}{}
	for _, result := range backup.BacktestResults {
		resultIDs[result.ID] = struct{}{}
	}
	for _, summary := range backup.BacktestSummaries {
		resultIDs[summary.BacktestResultID] = struct{}{}
	}
	for _, block := range backup.BacktestPathBlocks {
		resultIDs[block.BacktestResultID] = struct{}{}
	}
	for _, run := range backup.BacktestRuns {
		if run.BacktestResultID != nil {
			resultIDs[*run.BacktestResultID] = struct{}{}
		}
	}
	if len(resultIDs) > 0 {
		var results []saasstore.BacktestResult
		if err := db.Where("id IN ?", uintKeys(resultIDs)).Find(&results).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeBacktestResults(backup.BacktestResults, results)
	}

	specIDs := map[uint]struct{}{}
	for _, spec := range backup.BacktestSpecs {
		specIDs[spec.ID] = struct{}{}
	}
	resultIDs = map[uint]struct{}{}
	for _, result := range backup.BacktestResults {
		resultIDs[result.ID] = struct{}{}
		specIDs[result.BacktestSpecID] = struct{}{}
	}
	if len(specIDs) > 0 {
		var specs []saasstore.BacktestSpec
		if err := db.Where("id IN ?", uintKeys(specIDs)).Find(&specs).Error; err != nil {
			return err
		}
		backup.BacktestSpecs = mergeBacktestSpecs(backup.BacktestSpecs, specs)
	}
	if len(resultIDs) > 0 {
		ids := uintKeys(resultIDs)
		var summaries []saasstore.BacktestResultSummary
		if err := db.Where("backtest_result_id IN ?", ids).Find(&summaries).Error; err != nil {
			return err
		}
		backup.BacktestSummaries = mergeBacktestSummaries(backup.BacktestSummaries, summaries)
		var blocks []saasstore.BacktestPathBlock
		if err := db.Where("backtest_result_id IN ?", ids).Find(&blocks).Error; err != nil {
			return err
		}
		backup.BacktestPathBlocks = mergeBacktestPathBlocks(backup.BacktestPathBlocks, blocks)
	}
	return nil
}

func hydratePerformanceClosure(db *gorm.DB, backup *incrementalBackup) error {
	reportIDs := map[uint]struct{}{}
	for _, report := range backup.PerformanceReports {
		reportIDs[report.ID] = struct{}{}
	}
	for _, summary := range backup.PerformanceSummaries {
		reportIDs[summary.PerformanceReportID] = struct{}{}
	}
	for _, chart := range backup.PerformanceCharts {
		reportIDs[chart.PerformanceReportID] = struct{}{}
	}
	resultIDs := map[uint]struct{}{}
	for _, result := range backup.BacktestResults {
		resultIDs[result.ID] = struct{}{}
	}
	if len(resultIDs) > 0 {
		ids := uintKeys(resultIDs)
		var reports []saasstore.PerformanceReport
		if err := db.Where("backtest_result_id IN ? OR annualization_backtest_result_id IN ?", ids, ids).Find(&reports).Error; err != nil {
			return err
		}
		backup.PerformanceReports = mergePerformanceReports(backup.PerformanceReports, reports)
		for _, report := range reports {
			reportIDs[report.ID] = struct{}{}
		}
	}
	if len(reportIDs) > 0 {
		ids := uintKeys(reportIDs)
		var reports []saasstore.PerformanceReport
		if err := db.Where("id IN ?", ids).Find(&reports).Error; err != nil {
			return err
		}
		backup.PerformanceReports = mergePerformanceReports(backup.PerformanceReports, reports)
		for _, report := range backup.PerformanceReports {
			resultIDs[report.BacktestResultID] = struct{}{}
			resultIDs[report.AnnualizationBacktestResultID] = struct{}{}
			reportIDs[report.ID] = struct{}{}
		}
		var summaries []saasstore.PerformanceReportSummary
		if err := db.Where("performance_report_id IN ?", uintKeys(reportIDs)).Find(&summaries).Error; err != nil {
			return err
		}
		backup.PerformanceSummaries = mergePerformanceSummaries(backup.PerformanceSummaries, summaries)
		var charts []saasstore.PerformanceReportChartBlock
		if err := db.Where("performance_report_id IN ?", uintKeys(reportIDs)).Find(&charts).Error; err != nil {
			return err
		}
		backup.PerformanceCharts = mergePerformanceCharts(backup.PerformanceCharts, charts)
	}
	if len(resultIDs) > 0 {
		var results []saasstore.BacktestResult
		if err := db.Where("id IN ?", uintKeys(resultIDs)).Find(&results).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeBacktestResults(backup.BacktestResults, results)
	}
	return nil
}

func hydrateComputeClosure(db *gorm.DB, backup *incrementalBackup) error {
	for pass := 0; pass < 1000; pass++ {
		before := len(backup.ComputeTasks) + len(backup.ComputeTaskItems) + len(backup.ComputeCacheEntries) + len(backup.ComputeDependencies)
		taskIDs := map[uint]struct{}{}
		itemIDs := map[uint]struct{}{}
		cacheIDs := map[uint]struct{}{}
		for _, task := range backup.ComputeTasks {
			taskIDs[task.ID] = struct{}{}
			if task.ParentTaskID != nil {
				taskIDs[*task.ParentTaskID] = struct{}{}
			}
		}
		for _, item := range backup.ComputeTaskItems {
			itemIDs[item.ID] = struct{}{}
			taskIDs[item.ComputeTaskID] = struct{}{}
			if item.CacheEntryID != nil {
				cacheIDs[*item.CacheEntryID] = struct{}{}
			}
		}
		for _, entry := range backup.ComputeCacheEntries {
			cacheIDs[entry.ID] = struct{}{}
			if entry.SourceTaskItemID != nil {
				itemIDs[*entry.SourceTaskItemID] = struct{}{}
			}
		}
		for _, dependency := range backup.ComputeDependencies {
			taskIDs[dependency.ComputeTaskID] = struct{}{}
			taskIDs[dependency.DependsOnTaskID] = struct{}{}
		}

		if len(itemIDs) > 0 {
			var rows []saasstore.ComputeTaskItem
			if err := db.Where("id IN ?", uintKeys(itemIDs)).Find(&rows).Error; err != nil {
				return err
			}
			backup.ComputeTaskItems = mergeComputeTaskItems(backup.ComputeTaskItems, rows)
			for _, item := range rows {
				taskIDs[item.ComputeTaskID] = struct{}{}
				if item.CacheEntryID != nil {
					cacheIDs[*item.CacheEntryID] = struct{}{}
				}
			}
		}
		if len(taskIDs) > 0 {
			ids := uintKeys(taskIDs)
			var rows []saasstore.ComputeTask
			if err := db.Where("id IN ? OR parent_task_id IN ?", ids, ids).Find(&rows).Error; err != nil {
				return err
			}
			backup.ComputeTasks = mergeComputeTasks(backup.ComputeTasks, rows)
			for _, task := range rows {
				taskIDs[task.ID] = struct{}{}
				if task.ParentTaskID != nil {
					taskIDs[*task.ParentTaskID] = struct{}{}
				}
			}
		}
		if len(taskIDs) > 0 {
			ids := uintKeys(taskIDs)
			var dependencies []saasstore.ComputeTaskDependency
			if err := db.Where("compute_task_id IN ? OR depends_on_task_id IN ?", ids, ids).Find(&dependencies).Error; err != nil {
				return err
			}
			backup.ComputeDependencies = mergeComputeDependencies(backup.ComputeDependencies, dependencies)
			for _, dependency := range dependencies {
				taskIDs[dependency.ComputeTaskID] = struct{}{}
				taskIDs[dependency.DependsOnTaskID] = struct{}{}
			}
			var items []saasstore.ComputeTaskItem
			if err := db.Where("compute_task_id IN ?", ids).Find(&items).Error; err != nil {
				return err
			}
			backup.ComputeTaskItems = mergeComputeTaskItems(backup.ComputeTaskItems, items)
			for _, item := range items {
				if item.CacheEntryID != nil {
					cacheIDs[*item.CacheEntryID] = struct{}{}
				}
			}
		}
		if len(cacheIDs) > 0 {
			var rows []saasstore.ComputeCacheEntry
			if err := db.Where("id IN ?", uintKeys(cacheIDs)).Find(&rows).Error; err != nil {
				return err
			}
			backup.ComputeCacheEntries = mergeComputeCacheEntries(backup.ComputeCacheEntries, rows)
		}
		after := len(backup.ComputeTasks) + len(backup.ComputeTaskItems) + len(backup.ComputeCacheEntries) + len(backup.ComputeDependencies)
		if after == before {
			sortComputeTasksForRestore(backup.ComputeTasks)
			return nil
		}
	}
	return fmt.Errorf("計算任務備份引用閉包超過安全深度")
}

func mergeComputeTasks(left []saasstore.ComputeTask, right []saasstore.ComputeTask) []saasstore.ComputeTask {
	byID := make(map[uint]saasstore.ComputeTask, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.ComputeTask, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sortComputeTasksForRestore(rows)
	return rows
}

func sortComputeTasksForRestore(rows []saasstore.ComputeTask) {
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].ParentTaskID == nil) != (rows[j].ParentTaskID == nil) {
			return rows[i].ParentTaskID == nil
		}
		if rows[i].StageOrder != rows[j].StageOrder {
			return rows[i].StageOrder < rows[j].StageOrder
		}
		return rows[i].ID < rows[j].ID
	})
}

func mergeComputeTaskItems(left []saasstore.ComputeTaskItem, right []saasstore.ComputeTaskItem) []saasstore.ComputeTaskItem {
	byID := make(map[uint]saasstore.ComputeTaskItem, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.ComputeTaskItem, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ComputeTaskID == rows[j].ComputeTaskID {
			return rows[i].ItemIndex < rows[j].ItemIndex
		}
		return rows[i].ComputeTaskID < rows[j].ComputeTaskID
	})
	return rows
}

func mergeComputeCacheEntries(left []saasstore.ComputeCacheEntry, right []saasstore.ComputeCacheEntry) []saasstore.ComputeCacheEntry {
	byID := make(map[uint]saasstore.ComputeCacheEntry, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.ComputeCacheEntry, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeComputeDependencies(left []saasstore.ComputeTaskDependency, right []saasstore.ComputeTaskDependency) []saasstore.ComputeTaskDependency {
	byID := make(map[uint]saasstore.ComputeTaskDependency, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.ComputeTaskDependency, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func verifyRestoredBacktests(db *gorm.DB, backup incrementalBackup) error {
	if len(backup.BacktestResults) == 0 {
		return nil
	}
	resultStore := backtestresult.NewStore(db)
	for _, result := range backup.BacktestResults {
		report, err := resultStore.VerifyResult(context.Background(), result.ID)
		if err != nil {
			return fmt.Errorf("還原後標準化回測結果 %d 驗證失敗: %w", result.ID, err)
		}
		if !report.Valid {
			return fmt.Errorf("還原後標準化回測結果 %d 驗證未通過", result.ID)
		}
	}
	return nil
}

func verifyRestoredPerformanceReports(db *gorm.DB, backup incrementalBackup) error {
	if len(backup.PerformanceReports) == 0 {
		return nil
	}
	store := performancereport.NewStore(db)
	for _, saved := range backup.PerformanceReports {
		report, err := store.Verify(context.Background(), saved.ID, true)
		if err != nil {
			return fmt.Errorf("還原後報酬分析報告 %d 驗證失敗: %w", saved.ID, err)
		}
		if !report.Valid {
			return fmt.Errorf("還原後報酬分析報告 %d 驗證未通過", saved.ID)
		}
	}
	return nil
}

func verifyRestoredComputeTasks(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.ComputeCacheEntries {
		var entry saasstore.ComputeCacheEntry
		if err := db.First(&entry, saved.ID).Error; err != nil {
			return fmt.Errorf("還原後計算快取 %d 不存在: %w", saved.ID, err)
		}
		if entry.CacheKey != saved.CacheKey || entry.OwnerUserID != saved.OwnerUserID {
			return fmt.Errorf("還原後計算快取 %d 身分不一致", saved.ID)
		}
		if entry.Status == compute.CacheStatusCompleted {
			if err := verifyJSONContentHash(entry.Result, entry.ContentHash); err != nil {
				return fmt.Errorf("還原後計算快取 %d 驗證失敗: %w", entry.ID, err)
			}
		}
	}

	for _, saved := range backup.ComputeTasks {
		var task saasstore.ComputeTask
		if err := db.First(&task, saved.ID).Error; err != nil {
			return fmt.Errorf("還原後計算任務 %d 不存在: %w", saved.ID, err)
		}
		if task.PlanKey != saved.PlanKey || task.UserID != saved.UserID || task.Kind != saved.Kind {
			return fmt.Errorf("還原後計算任務 %d 身分不一致", saved.ID)
		}
		if err := verifyJSONContentHash(task.Settings, task.SettingsHash); err != nil {
			return fmt.Errorf("還原後計算任務 %d 設定驗證失敗: %w", task.ID, err)
		}
		if err := verifyJSONContentHash(task.Manifest, task.ManifestHash); err != nil {
			return fmt.Errorf("還原後計算任務 %d manifest 驗證失敗: %w", task.ID, err)
		}
		if task.ParentTaskID != nil {
			var parent saasstore.ComputeTask
			if err := db.First(&parent, *task.ParentTaskID).Error; err != nil || parent.UserID != task.UserID || parent.Kind != compute.TaskKindComposite {
				return fmt.Errorf("還原後計算任務 %d 的父任務引用無效", task.ID)
			}
		}
		if task.Kind == compute.TaskKindComposite {
			if err := verifyRestoredCompositeTask(db, task); err != nil {
				return err
			}
			continue
		}
		if err := verifyRestoredAtomicTask(db, task); err != nil {
			return err
		}
	}

	for _, saved := range backup.ComputeDependencies {
		var dependency saasstore.ComputeTaskDependency
		if err := db.First(&dependency, saved.ID).Error; err != nil {
			return fmt.Errorf("還原後計算任務依賴 %d 不存在: %w", saved.ID, err)
		}
		var taskCount int64
		if err := db.Model(&saasstore.ComputeTask{}).Where("id IN ?", []uint{dependency.ComputeTaskID, dependency.DependsOnTaskID}).Count(&taskCount).Error; err != nil || taskCount != 2 {
			return fmt.Errorf("還原後計算任務依賴 %d 引用無效", saved.ID)
		}
	}
	return nil
}

func verifyRestoredAtomicTask(db *gorm.DB, task saasstore.ComputeTask) error {
	var manifest compute.Manifest
	if err := json.Unmarshal(task.Manifest, &manifest); err != nil {
		return fmt.Errorf("還原後計算任務 %d manifest 無法解析: %w", task.ID, err)
	}
	if manifest.SchemaVersion != task.ManifestVersion || manifest.TotalItems != task.TotalItems || len(manifest.Items) != task.TotalItems {
		return fmt.Errorf("還原後計算任務 %d manifest 計數或版本不一致", task.ID)
	}
	var items []saasstore.ComputeTaskItem
	if err := db.Where("compute_task_id = ?", task.ID).Order("item_index ASC").Find(&items).Error; err != nil {
		return err
	}
	if len(items) != len(manifest.Items) {
		return fmt.Errorf("還原後計算任務 %d 項目不完整", task.ID)
	}
	inputs := make([]compute.ManifestItemInput, 0, len(items))
	for index, item := range items {
		expected := manifest.Items[index]
		if item.ItemIndex != expected.Index || item.ItemKey != expected.Key || item.CacheKey != expected.ResolvedCacheKey ||
			item.BaseCacheKey != expected.BaseCacheKey || item.InputHash != expected.InputHash {
			return fmt.Errorf("還原後計算任務 %d 項目 %d 身分不一致", task.ID, item.ID)
		}
		if err := verifyJSONContentHash(item.Input, item.InputHash); err != nil {
			return fmt.Errorf("還原後計算任務項目 %d 輸入驗證失敗: %w", item.ID, err)
		}
		if item.Status == compute.ItemStatusCompleted || item.Status == compute.ItemStatusCached {
			if err := verifyJSONContentHash(item.Result, item.ResultHash); err != nil {
				return fmt.Errorf("還原後計算任務項目 %d 結果驗證失敗: %w", item.ID, err)
			}
		}
		if item.CacheEntryID != nil {
			var cache saasstore.ComputeCacheEntry
			if err := db.First(&cache, *item.CacheEntryID).Error; err != nil || cache.CacheKey != item.CacheKey {
				return fmt.Errorf("還原後計算任務項目 %d 快取引用無效", item.ID)
			}
			if (item.Status == compute.ItemStatusCompleted || item.Status == compute.ItemStatusCached) && cache.ContentHash != item.ResultHash {
				return fmt.Errorf("還原後計算任務項目 %d 與快取內容不一致", item.ID)
			}
		}
		inputs = append(inputs, compute.ManifestItemInput{Key: expected.Key, CacheKey: expected.BaseCacheKey, Input: expected.Input, EstimatedUnits: expected.EstimatedUnits})
	}
	parentPlanKey := ""
	if task.ParentTaskID != nil {
		if err := db.Model(&saasstore.ComputeTask{}).Where("id = ?", *task.ParentTaskID).Pluck("plan_key", &parentPlanKey).Error; err != nil {
			return err
		}
	}
	rebuilt, err := compute.BuildPlan(compute.PlanSpec{
		TaskType: task.TaskType,
		Executor: compute.ExecutorDescriptor{Type: task.ExecutorType, Version: task.ExecutorVersion, ResultSchemaVersion: task.ResultSchemaVersion},
		Settings: json.RawMessage(task.Settings), ResearchSettingHash: task.ResearchSettingHash,
		ParentPlanKey: parentPlanKey, StageKey: task.StageKey, StageType: task.StageType, StageOrder: task.StageOrder,
		RNG: compute.RNGSpec{Algorithm: task.RNGAlgorithm, Version: task.RNGVersion, RootSeed: task.RootSeed}, Items: inputs,
	})
	if err != nil || rebuilt.PlanKey != task.PlanKey || rebuilt.Snapshot.ManifestHash != task.ManifestHash {
		return fmt.Errorf("還原後計算任務 %d plan identity 驗證失敗", task.ID)
	}
	return nil
}

func verifyRestoredCompositeTask(db *gorm.DB, task saasstore.ComputeTask) error {
	var manifest compute.CompositeManifest
	if err := json.Unmarshal(task.Manifest, &manifest); err != nil {
		return fmt.Errorf("還原後複合任務 %d manifest 無法解析: %w", task.ID, err)
	}
	if manifest.SchemaVersion != task.ManifestVersion {
		return fmt.Errorf("還原後複合任務 %d manifest 版本不一致", task.ID)
	}
	var children []saasstore.ComputeTask
	if err := db.Where("parent_task_id = ?", task.ID).Find(&children).Error; err != nil {
		return err
	}
	if len(children) != len(manifest.Stages) {
		return fmt.Errorf("還原後複合任務 %d 階段不完整", task.ID)
	}
	byKey := make(map[string]saasstore.ComputeTask, len(children))
	for _, child := range children {
		byKey[child.StageKey] = child
	}
	for _, stage := range manifest.Stages {
		child, ok := byKey[stage.Key]
		if !ok || child.StageType != stage.Type || child.StageOrder != stage.Order || child.SettingsHash != stage.SettingsHash ||
			child.ManifestHash != stage.ManifestHash || child.ExecutorType != stage.Executor.Type || child.ExecutorVersion != stage.Executor.Version ||
			child.ResultSchemaVersion != stage.Executor.ResultSchemaVersion {
			return fmt.Errorf("還原後複合任務 %d 階段 %s 身分不一致", task.ID, stage.Key)
		}
		var dependencyKeys []string
		if err := db.Table("compute_task_dependencies AS dependency").
			Select("prerequisite.stage_key").
			Joins("JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id").
			Where("dependency.compute_task_id = ?", child.ID).Order("prerequisite.stage_key ASC").Scan(&dependencyKeys).Error; err != nil {
			return err
		}
		expectedKeys := append([]string(nil), stage.DependsOnStageKeys...)
		sort.Strings(expectedKeys)
		if strings.Join(dependencyKeys, "\x00") != strings.Join(expectedKeys, "\x00") {
			return fmt.Errorf("還原後複合任務 %d 階段 %s 依賴不一致", task.ID, stage.Key)
		}
	}
	return nil
}

func verifyJSONContentHash(raw []byte, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("缺少內容 hash")
	}
	canonical, err := compute.CanonicalRawJSON(raw)
	if err != nil {
		return err
	}
	if compute.HashBytes(canonical) != expected {
		return fmt.Errorf("內容 hash 不一致")
	}
	return nil
}

func uintKeys(values map[uint]struct{}) []uint {
	keys := make([]uint, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i int, j int) bool { return keys[i] < keys[j] })
	return keys
}

func mergeBacktestSpecs(left []saasstore.BacktestSpec, right []saasstore.BacktestSpec) []saasstore.BacktestSpec {
	byID := make(map[uint]saasstore.BacktestSpec, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.BacktestSpec, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i int, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeBacktestResults(left []saasstore.BacktestResult, right []saasstore.BacktestResult) []saasstore.BacktestResult {
	byID := make(map[uint]saasstore.BacktestResult, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.BacktestResult, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i int, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeBacktestSummaries(left []saasstore.BacktestResultSummary, right []saasstore.BacktestResultSummary) []saasstore.BacktestResultSummary {
	byID := make(map[uint]saasstore.BacktestResultSummary, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.BacktestResultSummary, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i int, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeBacktestPathBlocks(left []saasstore.BacktestPathBlock, right []saasstore.BacktestPathBlock) []saasstore.BacktestPathBlock {
	byID := make(map[uint]saasstore.BacktestPathBlock, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.BacktestPathBlock, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i int, j int) bool {
		if rows[i].BacktestResultID == rows[j].BacktestResultID {
			return rows[i].BlockIndex < rows[j].BlockIndex
		}
		return rows[i].BacktestResultID < rows[j].BacktestResultID
	})
	return rows
}

func mergePerformanceReports(left []saasstore.PerformanceReport, right []saasstore.PerformanceReport) []saasstore.PerformanceReport {
	byID := make(map[uint]saasstore.PerformanceReport, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.PerformanceReport, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergePerformanceSummaries(left []saasstore.PerformanceReportSummary, right []saasstore.PerformanceReportSummary) []saasstore.PerformanceReportSummary {
	byID := make(map[uint]saasstore.PerformanceReportSummary, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.PerformanceReportSummary, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergePerformanceCharts(left []saasstore.PerformanceReportChartBlock, right []saasstore.PerformanceReportChartBlock) []saasstore.PerformanceReportChartBlock {
	byID := make(map[uint]saasstore.PerformanceReportChartBlock, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.PerformanceReportChartBlock, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PerformanceReportID == rows[j].PerformanceReportID {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].PerformanceReportID < rows[j].PerformanceReportID
	})
	return rows
}

func changedSince[T any](db *gorm.DB, since time.Time, out *[]T) error {
	return db.Where("created_at >= ? OR updated_at >= ?", since, since).Find(out).Error
}

func changedSinceUnscoped[T any](db *gorm.DB, since time.Time, out *[]T) error {
	return db.Unscoped().Where("created_at >= ? OR updated_at >= ? OR deleted_at >= ?", since, since, since).Find(out).Error
}

func createdSince[T any](db *gorm.DB, since time.Time, out *[]T) error {
	return db.Where("created_at >= ?", since).Find(out).Error
}

func saveAll[T any](db *gorm.DB, rows []T) error {
	for i := range rows {
		if err := db.Omit(clause.Associations).Save(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func saveAllUnscoped[T any](db *gorm.DB, rows []T) error {
	for i := range rows {
		if err := db.Unscoped().Omit(clause.Associations).Save(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetSequences(db *gorm.DB) error {
	tables := map[string]string{
		"k_lines":                         "id",
		"dataset_metadata":                "id",
		"daily_execution_snapshots":       "id",
		"gene_records":                    "id",
		"gene_observations":               "id",
		"evolution_tasks":                 "id",
		"backtest_specs":                  "id",
		"backtest_results":                "id",
		"backtest_result_summaries":       "id",
		"backtest_path_blocks":            "id",
		"backtest_runs":                   "id",
		"performance_reports":             "id",
		"performance_report_summaries":    "id",
		"performance_report_chart_blocks": "id",
		"compute_tasks":                   "id",
		"compute_cache_entries":           "id",
		"compute_task_items":              "id",
		"compute_task_dependencies":       "id",
		"market_series":                   "id",
		"market_data_versions":            "id",
		"market_data_version_bars":        "id",
		"market_data_version_sources":     "id",
		"recomposition_plans":             "id",
		"recomposition_plan_segments":     "id",
		"recomposition_preview_bars":      "id",
		"recomposition_generations":       "id",
		"recomposition_segment_instances": "id",
		"recomposition_bar_lineages":      "id",
		"research_datasets":               "id",
		"research_dataset_series":         "id",
	}
	for table, column := range tables {
		sql := fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 1), true)", table, column, column, table)
		if err := db.Exec(sql).Error; err != nil {
			return err
		}
	}
	return nil
}

func openDB(dsn string, configPath string) (*gorm.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	}
	if strings.TrimSpace(dsn) == "" && strings.TrimSpace(configPath) != "" {
		cfg, err := config.Load(configPath)
		if err == nil {
			dsn = cfg.Database.DSN
		}
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("請提供 --dsn 或設定 DATABASE_DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("連線資料庫失敗: %w", err)
	}
	return db, nil
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("無法解析時間: %s", value)
}

func ensureParentDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func usage() error {
	fmt.Println(`QuantSaaS 備份工具

用法：
  backup-tool export-incremental --since 2026-06-22T00:00:00Z --out backups/work/incremental.json
  backup-tool import-incremental --in backups/work/incremental.json
  backup-tool verify-backtests
  backup-tool verify-compute-tasks
  backup-tool backtest-references --id 123
  backup-tool archive-backtest --id 123
  backup-tool invalidate-backtest --id 123 --reason "dataset superseded"
  backup-tool delete-backtest-path --id 123 [--allow-referenced]
  backup-tool delete-failed-backtest --id 123

環境：
  需要 --dsn 或 DATABASE_DSN。此工具只做資料匯出/匯入，不啟動交易功能。`)
	return nil
}
