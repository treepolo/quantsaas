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
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	"quantsaas/internal/saas/performancereport"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const backupVersion = 9

type incrementalBackup struct {
	Version                  int                                       `json:"version"`
	Kind                     string                                    `json:"kind"`
	CreatedAt                string                                    `json:"created_at"`
	Since                    string                                    `json:"since"`
	ResearchInstruments      []saasstore.ResearchInstrument            `json:"research_instruments"`
	KLines                   []saasstore.KLine                         `json:"k_lines"`
	DatasetMetadata          []saasstore.DatasetMetadata               `json:"dataset_metadata"`
	DailySnapshots           []saasstore.DailyExecutionSnapshot        `json:"daily_execution_snapshots"`
	GeneRecords              []saasstore.GeneRecord                    `json:"gene_records"`
	GeneObservations         []saasstore.GeneObservation               `json:"gene_observations"`
	EvolutionTasks           []saasstore.EvolutionTask                 `json:"evolution_tasks"`
	BacktestSpecs            []saasstore.BacktestSpec                  `json:"backtest_specs"`
	BacktestResults          []saasstore.BacktestResult                `json:"backtest_results"`
	BacktestSummaries        []saasstore.BacktestResultSummary         `json:"backtest_result_summaries"`
	BacktestPathBlocks       []saasstore.BacktestPathBlock             `json:"backtest_path_blocks"`
	BacktestRuns             []saasstore.BacktestRun                   `json:"backtest_runs"`
	PerformanceReports       []saasstore.PerformanceReport             `json:"performance_reports"`
	PerformanceSummaries     []saasstore.PerformanceReportSummary      `json:"performance_report_summaries"`
	PerformanceCharts        []saasstore.PerformanceReportChartBlock   `json:"performance_report_chart_blocks"`
	ComputeTasks             []saasstore.ComputeTask                   `json:"compute_tasks"`
	ComputeCacheEntries      []saasstore.ComputeCacheEntry             `json:"compute_cache_entries"`
	ComputeTaskItems         []saasstore.ComputeTaskItem               `json:"compute_task_items"`
	ComputeDependencies      []saasstore.ComputeTaskDependency         `json:"compute_task_dependencies"`
	RobustnessStudies        []saasstore.RobustnessStudy               `json:"robustness_studies"`
	RobustnessPoints         []saasstore.RobustnessEvaluationPoint     `json:"robustness_evaluation_points"`
	RobustnessSnapshots      []saasstore.RobustnessAnalysisSnapshot    `json:"robustness_analysis_snapshots"`
	DynamicModelStudies      []saasstore.DynamicModelStudy             `json:"dynamic_model_studies"`
	DynamicModelArtifacts    []saasstore.DynamicModelArtifact          `json:"dynamic_model_artifacts"`
	DynamicPredictions       []saasstore.DynamicPredictionSnapshot     `json:"dynamic_prediction_snapshots"`
	DynamicPolicies          []saasstore.DynamicPolicyArtifact         `json:"dynamic_policy_artifacts"`
	DynamicMaterializations  []saasstore.DynamicMaterialization        `json:"dynamic_materializations"`
	DynamicReportSnapshots   []saasstore.DynamicModelReportSnapshot    `json:"dynamic_model_report_snapshots"`
	DynamicReportBlocks      []saasstore.DynamicReportBlock            `json:"dynamic_report_blocks"`
	MarketSeries             []saasstore.MarketSeries                  `json:"market_series"`
	MarketDataVersions       []saasstore.MarketDataVersion             `json:"market_data_versions"`
	MarketVersionBars        []saasstore.MarketDataVersionBar          `json:"market_data_version_bars"`
	MarketVersionSources     []saasstore.MarketDataVersionSource       `json:"market_data_version_sources"`
	RecompositionPlans       []saasstore.RecompositionPlan             `json:"recomposition_plans"`
	RecompositionSegments    []saasstore.RecompositionPlanSegment      `json:"recomposition_plan_segments"`
	RecompositionPreviewBars []saasstore.RecompositionPreviewBar       `json:"recomposition_preview_bars"`
	RecompositionGenerations []saasstore.RecompositionGeneration       `json:"recomposition_generations"`
	RecompositionInstances   []saasstore.RecompositionSegmentInstance  `json:"recomposition_segment_instances"`
	RecompositionLineage     []saasstore.RecompositionBarLineage       `json:"recomposition_bar_lineage"`
	ResearchDatasets         []saasstore.ResearchDataset               `json:"research_datasets"`
	ResearchDatasetSeries    []saasstore.ResearchDatasetSeries         `json:"research_dataset_series"`
	ResearchConfigurations   []saasstore.ResearchConfiguration         `json:"research_configurations"`
	ResearchConfigMetadata   []saasstore.ResearchConfigurationMetadata `json:"research_configuration_metadata"`
	ResearchRuns             []saasstore.ResearchRun                   `json:"research_runs"`
	ResearchStages           []saasstore.ResearchStage                 `json:"research_stages"`
	ResearchPoints           []saasstore.ResearchEvaluationPoint       `json:"research_evaluation_points"`
	ResearchPointOrigins     []saasstore.ResearchPointOrigin           `json:"research_point_origins"`
	ResearchAnalyses         []saasstore.ResearchAnalysisSnapshot      `json:"research_analysis_snapshots"`
	RobustRegions            []saasstore.RobustRegion                  `json:"robust_regions"`
	RobustRegionPoints       []saasstore.RobustRegionPoint             `json:"robust_region_points"`
	RobustCandidates         []saasstore.RobustCandidate               `json:"robust_candidates"`
	CandidateAnalysisLinks   []saasstore.CandidateAnalysisLink         `json:"candidate_analysis_links"`
	CandidateGeneLinks       []saasstore.CandidateGeneLink             `json:"candidate_gene_links"`
	ResearchSeries           []saasstore.ResearchSeries                `json:"research_series"`
	ResearchSeriesMembers    []saasstore.ResearchSeriesMember          `json:"research_series_members"`
	ResearchComparisons      []saasstore.ResearchComparisonSnapshot    `json:"research_comparison_snapshots"`
	SurrogateSnapshots       []saasstore.SurrogateModelSnapshot        `json:"surrogate_model_snapshots"`
	SurrogateProposals       []saasstore.SurrogateProposal             `json:"surrogate_proposals"`
	RandomParameterBatches   []saasstore.RandomParameterBatch          `json:"random_parameter_batches"`
	RandomParameterRecords   []saasstore.RandomParameterRecord         `json:"random_parameter_records"`
	ControlAnalysisTasks     []saasstore.ControlAnalysisTask           `json:"control_analysis_tasks"`
	ControlEvaluations       []saasstore.ControlEvaluation             `json:"control_evaluations"`
	ControlSnapshots         []saasstore.ControlAnalysisSnapshot       `json:"control_analysis_snapshots"`
	ControlSnapshotMembers   []saasstore.ControlSnapshotMember         `json:"control_snapshot_members"`
	Counts                   map[string]int                            `json:"counts"`
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
	if err := changedSince(db, since, &backup.RobustnessStudies); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.RobustnessPoints); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.RobustnessSnapshots); err != nil {
		return backup, err
	}
	if err := changedSince(db, since, &backup.DynamicModelStudies); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicModelArtifacts); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicPredictions); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicPolicies); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicMaterializations); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicReportSnapshots); err != nil {
		return backup, err
	}
	if err := createdSince(db, since, &backup.DynamicReportBlocks); err != nil {
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
	for _, target := range []any{&backup.ResearchConfigurations, &backup.ResearchConfigMetadata, &backup.ResearchRuns, &backup.ResearchStages, &backup.ResearchPoints, &backup.RobustCandidates, &backup.CandidateAnalysisLinks, &backup.CandidateGeneLinks, &backup.ResearchSeries, &backup.SurrogateSnapshots} {
		if err := changedSinceAny(db, since, target); err != nil {
			return backup, err
		}
	}
	for _, target := range []any{&backup.ResearchPointOrigins, &backup.ResearchAnalyses, &backup.RobustRegions, &backup.RobustRegionPoints, &backup.ResearchSeriesMembers, &backup.ResearchComparisons, &backup.SurrogateProposals} {
		if err := createdSinceAny(db, since, target); err != nil {
			return backup, err
		}
	}
	for _, target := range []any{&backup.RandomParameterBatches, &backup.ControlAnalysisTasks, &backup.ControlEvaluations} {
		if err := changedSinceAny(db, since, target); err != nil {
			return backup, err
		}
	}
	for _, target := range []any{&backup.RandomParameterRecords, &backup.ControlSnapshots, &backup.ControlSnapshotMembers} {
		if err := createdSinceAny(db, since, target); err != nil {
			return backup, err
		}
	}
	if err := hydrateControlResearchClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateParameterResearchClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateMarketVersionClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateRobustnessClosure(db, &backup); err != nil {
		return backup, err
	}
	if err := hydrateDynamicModelClosure(db, &backup); err != nil {
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
	backup.Counts["robustness_studies"] = len(backup.RobustnessStudies)
	backup.Counts["robustness_evaluation_points"] = len(backup.RobustnessPoints)
	backup.Counts["robustness_analysis_snapshots"] = len(backup.RobustnessSnapshots)
	backup.Counts["dynamic_model_studies"] = len(backup.DynamicModelStudies)
	backup.Counts["dynamic_model_artifacts"] = len(backup.DynamicModelArtifacts)
	backup.Counts["dynamic_prediction_snapshots"] = len(backup.DynamicPredictions)
	backup.Counts["dynamic_policy_artifacts"] = len(backup.DynamicPolicies)
	backup.Counts["dynamic_materializations"] = len(backup.DynamicMaterializations)
	backup.Counts["dynamic_model_report_snapshots"] = len(backup.DynamicReportSnapshots)
	backup.Counts["dynamic_report_blocks"] = len(backup.DynamicReportBlocks)
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
	backup.Counts["research_configurations"] = len(backup.ResearchConfigurations)
	backup.Counts["research_configuration_metadata"] = len(backup.ResearchConfigMetadata)
	backup.Counts["research_runs"] = len(backup.ResearchRuns)
	backup.Counts["research_stages"] = len(backup.ResearchStages)
	backup.Counts["research_evaluation_points"] = len(backup.ResearchPoints)
	backup.Counts["research_point_origins"] = len(backup.ResearchPointOrigins)
	backup.Counts["research_analysis_snapshots"] = len(backup.ResearchAnalyses)
	backup.Counts["robust_regions"] = len(backup.RobustRegions)
	backup.Counts["robust_region_points"] = len(backup.RobustRegionPoints)
	backup.Counts["robust_candidates"] = len(backup.RobustCandidates)
	backup.Counts["candidate_analysis_links"] = len(backup.CandidateAnalysisLinks)
	backup.Counts["candidate_gene_links"] = len(backup.CandidateGeneLinks)
	backup.Counts["research_series"] = len(backup.ResearchSeries)
	backup.Counts["research_series_members"] = len(backup.ResearchSeriesMembers)
	backup.Counts["research_comparison_snapshots"] = len(backup.ResearchComparisons)
	backup.Counts["surrogate_model_snapshots"] = len(backup.SurrogateSnapshots)
	backup.Counts["surrogate_proposals"] = len(backup.SurrogateProposals)
	backup.Counts["random_parameter_batches"] = len(backup.RandomParameterBatches)
	backup.Counts["random_parameter_records"] = len(backup.RandomParameterRecords)
	backup.Counts["control_analysis_tasks"] = len(backup.ControlAnalysisTasks)
	backup.Counts["control_evaluations"] = len(backup.ControlEvaluations)
	backup.Counts["control_analysis_snapshots"] = len(backup.ControlSnapshots)
	backup.Counts["control_snapshot_members"] = len(backup.ControlSnapshotMembers)
	return backup, nil
}

func hydrateControlResearchClosure(db *gorm.DB, backup *incrementalBackup) error {
	changed := len(backup.RandomParameterBatches)+len(backup.RandomParameterRecords)+len(backup.ControlAnalysisTasks)+len(backup.ControlEvaluations)+len(backup.ControlSnapshots)+len(backup.ControlSnapshotMembers) > 0
	if !changed {
		return nil
	}
	for _, target := range []any{&backup.RandomParameterBatches, &backup.RandomParameterRecords, &backup.ControlAnalysisTasks, &backup.ControlEvaluations, &backup.ControlSnapshots, &backup.ControlSnapshotMembers} {
		if err := db.Find(target).Error; err != nil {
			return err
		}
	}
	geneIDs, candidateIDs, configurationIDs := map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}
	taskIDs, resultIDs, reportIDs := map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}
	for _, row := range backup.ControlAnalysisTasks {
		if row.SourceGenomeID != nil {
			geneIDs[*row.SourceGenomeID] = struct{}{}
		}
		if row.CandidateID != nil {
			candidateIDs[*row.CandidateID] = struct{}{}
		}
		if row.ResearchConfigurationID != nil {
			configurationIDs[*row.ResearchConfigurationID] = struct{}{}
		}
		if row.ComputeTaskID != nil {
			taskIDs[*row.ComputeTaskID] = struct{}{}
		}
	}
	for _, row := range backup.RandomParameterRecords {
		if row.BacktestResultID != nil {
			resultIDs[*row.BacktestResultID] = struct{}{}
		}
	}
	for _, row := range backup.ControlEvaluations {
		resultIDs[row.BacktestResultID] = struct{}{}
		if row.PerformanceReportID != nil {
			reportIDs[*row.PerformanceReportID] = struct{}{}
		}
	}
	if ids := uintSetValues(geneIDs); len(ids) > 0 {
		var rows []saasstore.GeneRecord
		if err := db.Unscoped().Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.GeneRecords = mergeByUintID(backup.GeneRecords, rows, func(row saasstore.GeneRecord) uint { return row.ID })
	}
	if ids := uintSetValues(candidateIDs); len(ids) > 0 {
		var rows []saasstore.RobustCandidate
		if err := db.Unscoped().Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.RobustCandidates = mergeByUintID(backup.RobustCandidates, rows, func(row saasstore.RobustCandidate) uint { return row.ID })
	}
	if ids := uintSetValues(configurationIDs); len(ids) > 0 {
		var rows []saasstore.ResearchConfiguration
		if err := db.Unscoped().Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.ResearchConfigurations = mergeByUintID(backup.ResearchConfigurations, rows, func(row saasstore.ResearchConfiguration) uint { return row.ID })
	}
	if ids := uintSetValues(taskIDs); len(ids) > 0 {
		var rows []saasstore.ComputeTask
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.ComputeTasks = mergeByUintID(backup.ComputeTasks, rows, func(row saasstore.ComputeTask) uint { return row.ID })
	}
	if ids := uintSetValues(resultIDs); len(ids) > 0 {
		var rows []saasstore.BacktestResult
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeByUintID(backup.BacktestResults, rows, func(row saasstore.BacktestResult) uint { return row.ID })
	}
	if ids := uintSetValues(reportIDs); len(ids) > 0 {
		var rows []saasstore.PerformanceReport
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.PerformanceReports = mergeByUintID(backup.PerformanceReports, rows, func(row saasstore.PerformanceReport) uint { return row.ID })
	}
	return nil
}

func hydrateParameterResearchClosure(db *gorm.DB, backup *incrementalBackup) error {
	changed := len(backup.ResearchConfigurations)+len(backup.ResearchConfigMetadata)+len(backup.ResearchRuns)+len(backup.ResearchStages)+len(backup.ResearchPoints)+len(backup.ResearchPointOrigins)+len(backup.ResearchAnalyses)+len(backup.RobustRegions)+len(backup.RobustRegionPoints)+len(backup.RobustCandidates)+len(backup.CandidateAnalysisLinks)+len(backup.CandidateGeneLinks)+len(backup.ResearchSeries)+len(backup.ResearchSeriesMembers)+len(backup.ResearchComparisons)+len(backup.SurrogateSnapshots)+len(backup.SurrogateProposals) > 0
	if !changed {
		return nil
	}
	for _, target := range []any{&backup.ResearchConfigurations, &backup.ResearchConfigMetadata, &backup.ResearchRuns, &backup.ResearchStages, &backup.ResearchPoints, &backup.ResearchPointOrigins, &backup.ResearchAnalyses, &backup.RobustRegions, &backup.RobustRegionPoints, &backup.RobustCandidates, &backup.CandidateAnalysisLinks, &backup.CandidateGeneLinks, &backup.ResearchSeries, &backup.ResearchSeriesMembers, &backup.ResearchComparisons, &backup.SurrogateSnapshots, &backup.SurrogateProposals} {
		if err := db.Find(target).Error; err != nil {
			return err
		}
	}
	geneIDs, taskIDs, resultIDs, robustnessStudyIDs, robustnessSnapshotIDs, dynamicStudyIDs, dynamicPolicyIDs := map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}
	for _, row := range backup.ResearchConfigurations {
		var canonical struct {
			GenomeID uint `json:"genome_id"`
		}
		if json.Unmarshal(row.Canonical, &canonical) == nil && canonical.GenomeID != 0 {
			geneIDs[canonical.GenomeID] = struct{}{}
		}
		if row.DynamicStudyID != nil {
			dynamicStudyIDs[*row.DynamicStudyID] = struct{}{}
		}
		if row.DynamicPolicyID != nil {
			dynamicPolicyIDs[*row.DynamicPolicyID] = struct{}{}
		}
	}
	for _, row := range backup.ResearchStages {
		if row.ComputeTaskID != nil {
			taskIDs[*row.ComputeTaskID] = struct{}{}
		}
	}
	for _, row := range backup.ResearchPoints {
		if row.BacktestResultID != nil {
			resultIDs[*row.BacktestResultID] = struct{}{}
		}
	}
	for _, row := range backup.ResearchAnalyses {
		robustnessStudyIDs[row.RobustnessStudyID] = struct{}{}
		robustnessSnapshotIDs[row.RobustnessSnapshotID] = struct{}{}
	}
	for _, row := range backup.CandidateGeneLinks {
		geneIDs[row.GeneRecordID] = struct{}{}
	}
	for _, row := range backup.SurrogateSnapshots {
		if row.ComputeTaskID != nil {
			taskIDs[*row.ComputeTaskID] = struct{}{}
		}
	}
	if ids := uintSetValues(geneIDs); len(ids) > 0 {
		var rows []saasstore.GeneRecord
		if err := db.Unscoped().Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.GeneRecords = mergeByUintID(backup.GeneRecords, rows, func(row saasstore.GeneRecord) uint { return row.ID })
	}
	if ids := uintSetValues(taskIDs); len(ids) > 0 {
		var rows []saasstore.ComputeTask
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.ComputeTasks = mergeByUintID(backup.ComputeTasks, rows, func(row saasstore.ComputeTask) uint { return row.ID })
	}
	if ids := uintSetValues(resultIDs); len(ids) > 0 {
		var rows []saasstore.BacktestResult
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeByUintID(backup.BacktestResults, rows, func(row saasstore.BacktestResult) uint { return row.ID })
	}
	if ids := uintSetValues(robustnessStudyIDs); len(ids) > 0 {
		var rows []saasstore.RobustnessStudy
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.RobustnessStudies = mergeByUintID(backup.RobustnessStudies, rows, func(row saasstore.RobustnessStudy) uint { return row.ID })
	}
	if ids := uintSetValues(robustnessSnapshotIDs); len(ids) > 0 {
		var rows []saasstore.RobustnessAnalysisSnapshot
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.RobustnessSnapshots = mergeByUintID(backup.RobustnessSnapshots, rows, func(row saasstore.RobustnessAnalysisSnapshot) uint { return row.ID })
	}
	if ids := uintSetValues(dynamicStudyIDs); len(ids) > 0 {
		var rows []saasstore.DynamicModelStudy
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.DynamicModelStudies = mergeByUintID(backup.DynamicModelStudies, rows, func(row saasstore.DynamicModelStudy) uint { return row.ID })
	}
	if ids := uintSetValues(dynamicPolicyIDs); len(ids) > 0 {
		var rows []saasstore.DynamicPolicyArtifact
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.DynamicPolicies = mergeByUintID(backup.DynamicPolicies, rows, func(row saasstore.DynamicPolicyArtifact) uint { return row.ID })
	}
	return nil
}

func hydrateRobustnessClosure(db *gorm.DB, backup *incrementalBackup) error {
	if len(backup.RobustnessStudies)+len(backup.RobustnessPoints)+len(backup.RobustnessSnapshots) == 0 {
		return nil
	}
	studyIDs := map[uint]struct{}{}
	for _, row := range backup.RobustnessStudies {
		studyIDs[row.ID] = struct{}{}
	}
	for _, row := range backup.RobustnessPoints {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.RobustnessSnapshots {
		studyIDs[row.StudyID] = struct{}{}
	}
	ids := uintSetValues(studyIDs)
	if len(ids) > 0 {
		var studies []saasstore.RobustnessStudy
		if err := db.Where("id IN ?", ids).Find(&studies).Error; err != nil {
			return err
		}
		backup.RobustnessStudies = mergeRobustnessStudies(backup.RobustnessStudies, studies)
		var points []saasstore.RobustnessEvaluationPoint
		if err := db.Where("study_id IN ?", ids).Find(&points).Error; err != nil {
			return err
		}
		backup.RobustnessPoints = mergeRobustnessPoints(backup.RobustnessPoints, points)
		var snapshots []saasstore.RobustnessAnalysisSnapshot
		if err := db.Where("study_id IN ?", ids).Find(&snapshots).Error; err != nil {
			return err
		}
		backup.RobustnessSnapshots = mergeRobustnessSnapshots(backup.RobustnessSnapshots, snapshots)
	}
	geneIDs, taskIDs, resultIDs := map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}
	for _, study := range backup.RobustnessStudies {
		if study.SourceGenomeID != nil {
			geneIDs[*study.SourceGenomeID] = struct{}{}
		}
		if study.ComputeTaskID != nil {
			taskIDs[*study.ComputeTaskID] = struct{}{}
		}
	}
	for _, point := range backup.RobustnessPoints {
		if point.BacktestResultID != nil {
			resultIDs[*point.BacktestResultID] = struct{}{}
		}
	}
	if ids := uintSetValues(geneIDs); len(ids) > 0 {
		var rows []saasstore.GeneRecord
		if err := db.Unscoped().Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.GeneRecords = mergeGeneRecords(backup.GeneRecords, rows)
	}
	if ids := uintSetValues(taskIDs); len(ids) > 0 {
		var rows []saasstore.ComputeTask
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.ComputeTasks = mergeComputeTasks(backup.ComputeTasks, rows)
	}
	if ids := uintSetValues(resultIDs); len(ids) > 0 {
		var rows []saasstore.BacktestResult
		if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeBacktestResults(backup.BacktestResults, rows)
	}
	return nil
}

func hydrateDynamicModelClosure(db *gorm.DB, backup *incrementalBackup) error {
	if len(backup.DynamicModelStudies)+len(backup.DynamicModelArtifacts)+len(backup.DynamicPredictions)+len(backup.DynamicPolicies)+len(backup.DynamicMaterializations)+len(backup.DynamicReportSnapshots)+len(backup.DynamicReportBlocks) == 0 {
		return nil
	}
	studyIDs := map[uint]struct{}{}
	for _, row := range backup.DynamicModelStudies {
		studyIDs[row.ID] = struct{}{}
	}
	for _, row := range backup.DynamicModelArtifacts {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.DynamicPredictions {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.DynamicPolicies {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.DynamicMaterializations {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.DynamicReportSnapshots {
		studyIDs[row.StudyID] = struct{}{}
	}
	for _, row := range backup.DynamicReportBlocks {
		studyIDs[row.StudyID] = struct{}{}
	}
	ids := uintSetValues(studyIDs)
	if len(ids) == 0 {
		return nil
	}
	var studies []saasstore.DynamicModelStudy
	if err := db.Where("id IN ?", ids).Find(&studies).Error; err != nil {
		return err
	}
	backup.DynamicModelStudies = mergeByUintID(backup.DynamicModelStudies, studies, func(row saasstore.DynamicModelStudy) uint { return row.ID })
	loadChildren := func(target any) error { return db.Where("study_id IN ?", ids).Find(target).Error }
	var artifacts []saasstore.DynamicModelArtifact
	if err := loadChildren(&artifacts); err != nil {
		return err
	}
	var predictions []saasstore.DynamicPredictionSnapshot
	if err := loadChildren(&predictions); err != nil {
		return err
	}
	var policies []saasstore.DynamicPolicyArtifact
	if err := loadChildren(&policies); err != nil {
		return err
	}
	var materializations []saasstore.DynamicMaterialization
	if err := loadChildren(&materializations); err != nil {
		return err
	}
	var reports []saasstore.DynamicModelReportSnapshot
	if err := loadChildren(&reports); err != nil {
		return err
	}
	var blocks []saasstore.DynamicReportBlock
	if err := loadChildren(&blocks); err != nil {
		return err
	}
	backup.DynamicModelArtifacts = mergeByUintID(backup.DynamicModelArtifacts, artifacts, func(row saasstore.DynamicModelArtifact) uint { return row.ID })
	backup.DynamicPredictions = mergeByUintID(backup.DynamicPredictions, predictions, func(row saasstore.DynamicPredictionSnapshot) uint { return row.ID })
	backup.DynamicPolicies = mergeByUintID(backup.DynamicPolicies, policies, func(row saasstore.DynamicPolicyArtifact) uint { return row.ID })
	backup.DynamicMaterializations = mergeByUintID(backup.DynamicMaterializations, materializations, func(row saasstore.DynamicMaterialization) uint { return row.ID })
	backup.DynamicReportSnapshots = mergeByUintID(backup.DynamicReportSnapshots, reports, func(row saasstore.DynamicModelReportSnapshot) uint { return row.ID })
	backup.DynamicReportBlocks = mergeByUintID(backup.DynamicReportBlocks, blocks, func(row saasstore.DynamicReportBlock) uint { return row.ID })
	taskIDs, backtestIDs, geneIDs := map[uint]struct{}{}, map[uint]struct{}{}, map[uint]struct{}{}
	for _, study := range backup.DynamicModelStudies {
		if study.ComputeTaskID != nil {
			taskIDs[*study.ComputeTaskID] = struct{}{}
		}
		if study.MaterializationTaskID != nil {
			taskIDs[*study.MaterializationTaskID] = struct{}{}
		}
		var setting dynamicparamsvc.StudySetting
		if json.Unmarshal(study.Settings, &setting) == nil && setting.Request.GenomeID != 0 {
			geneIDs[setting.Request.GenomeID] = struct{}{}
		}
		var klines []saasstore.KLine
		if err := db.Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", study.InstrumentID, study.DataSource, study.Symbol, study.Interval, study.TrainStartTimeMs, study.TrainEndTimeMs).Find(&klines).Error; err != nil {
			return err
		}
		backup.KLines = mergeKLines(backup.KLines, klines)
	}
	for _, materialization := range backup.DynamicMaterializations {
		if materialization.BacktestResultID != nil {
			backtestIDs[*materialization.BacktestResultID] = struct{}{}
		}
	}
	if len(taskIDs) > 0 {
		var tasks []saasstore.ComputeTask
		if err := db.Where("id IN ?", uintKeys(taskIDs)).Find(&tasks).Error; err != nil {
			return err
		}
		backup.ComputeTasks = mergeComputeTasks(backup.ComputeTasks, tasks)
	}
	if len(backtestIDs) > 0 {
		var results []saasstore.BacktestResult
		if err := db.Where("id IN ?", uintKeys(backtestIDs)).Find(&results).Error; err != nil {
			return err
		}
		backup.BacktestResults = mergeByUintID(backup.BacktestResults, results, func(row saasstore.BacktestResult) uint { return row.ID })
	}
	if len(geneIDs) > 0 {
		var genes []saasstore.GeneRecord
		if err := db.Unscoped().Where("id IN ?", uintKeys(geneIDs)).Find(&genes).Error; err != nil {
			return err
		}
		backup.GeneRecords = mergeByUintID(backup.GeneRecords, genes, func(row saasstore.GeneRecord) uint { return row.ID })
	}
	return nil
}

func mergeByUintID[T any](left, right []T, id func(T) uint) []T {
	rows := make(map[uint]T, len(left)+len(right))
	for _, row := range left {
		rows[id(row)] = row
	}
	for _, row := range right {
		rows[id(row)] = row
	}
	keys := make([]uint, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	result := make([]T, 0, len(keys))
	for _, key := range keys {
		result = append(result, rows[key])
	}
	return result
}

func uintSetValues(values map[uint]struct{}) []uint {
	result := make([]uint, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func mergeGeneRecords(left, right []saasstore.GeneRecord) []saasstore.GeneRecord {
	byID := make(map[uint]saasstore.GeneRecord, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.GeneRecord, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeRobustnessStudies(left, right []saasstore.RobustnessStudy) []saasstore.RobustnessStudy {
	byID := make(map[uint]saasstore.RobustnessStudy, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.RobustnessStudy, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeRobustnessPoints(left, right []saasstore.RobustnessEvaluationPoint) []saasstore.RobustnessEvaluationPoint {
	byID := make(map[uint]saasstore.RobustnessEvaluationPoint, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.RobustnessEvaluationPoint, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func mergeRobustnessSnapshots(left, right []saasstore.RobustnessAnalysisSnapshot) []saasstore.RobustnessAnalysisSnapshot {
	byID := make(map[uint]saasstore.RobustnessAnalysisSnapshot, len(left)+len(right))
	for _, row := range left {
		byID[row.ID] = row
	}
	for _, row := range right {
		byID[row.ID] = row
	}
	rows := make([]saasstore.RobustnessAnalysisSnapshot, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
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
		if err := saveAll(tx, backup.RobustnessStudies); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RobustnessPoints); err != nil {
			return err
		}
		if err := saveAll(tx, backup.RobustnessSnapshots); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicModelStudies); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicModelArtifacts); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicPredictions); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicPolicies); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicMaterializations); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicReportSnapshots); err != nil {
			return err
		}
		if err := saveAll(tx, backup.DynamicReportBlocks); err != nil {
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
		for _, save := range []func() error{
			func() error { return saveAll(tx, backup.ResearchConfigurations) },
			func() error { return saveAll(tx, backup.ResearchConfigMetadata) },
			func() error { return saveAll(tx, backup.ResearchRuns) },
			func() error { return saveAll(tx, backup.ResearchStages) },
			func() error { return saveAll(tx, backup.ResearchPoints) },
			func() error { return saveAll(tx, backup.ResearchPointOrigins) },
			func() error { return saveAll(tx, backup.ResearchAnalyses) },
			func() error { return saveAll(tx, backup.RobustRegions) },
			func() error { return saveAll(tx, backup.RobustRegionPoints) },
			func() error { return saveAll(tx, backup.RobustCandidates) },
			func() error { return saveAll(tx, backup.CandidateAnalysisLinks) },
			func() error { return saveAll(tx, backup.CandidateGeneLinks) },
			func() error { return saveAll(tx, backup.ResearchSeries) },
			func() error { return saveAll(tx, backup.ResearchSeriesMembers) },
			func() error { return saveAll(tx, backup.ResearchComparisons) },
			func() error { return saveAll(tx, backup.SurrogateSnapshots) },
			func() error { return saveAll(tx, backup.SurrogateProposals) },
			func() error { return saveAll(tx, backup.RandomParameterBatches) },
			func() error { return saveAll(tx, backup.RandomParameterRecords) },
			func() error { return saveAll(tx, backup.ControlAnalysisTasks) },
			func() error { return saveAll(tx, backup.ControlEvaluations) },
			func() error { return saveAll(tx, backup.ControlSnapshots) },
			func() error { return saveAll(tx, backup.ControlSnapshotMembers) },
		} {
			if err := save(); err != nil {
				return err
			}
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
		if err := verifyRestoredRobustness(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredDynamicModels(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredMarketVersions(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredParameterResearch(tx, backup); err != nil {
			return err
		}
		if err := verifyRestoredControlResearch(tx, backup); err != nil {
			return err
		}
		return resetSequences(tx)
	})
}

func verifyRestoredParameterResearch(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.ResearchConfigurations {
		var row saasstore.ResearchConfiguration
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ConfigHash != saved.ConfigHash || row.ParameterSpaceHash != saved.ParameterSpaceHash || row.DatasetHash != saved.DatasetHash {
			return fmt.Errorf("P10 研究設定 %d 還原後身分不一致", saved.ID)
		}
	}
	for _, saved := range backup.ResearchPoints {
		var row saasstore.ResearchEvaluationPoint
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.VectorHash != saved.VectorHash || row.MetricsHash != saved.MetricsHash || row.BacktestResultContentHash != saved.BacktestResultContentHash {
			return fmt.Errorf("P10 評估點 %d 還原後 hash 不一致", saved.ID)
		}
		if row.BacktestResultID != nil {
			var result saasstore.BacktestResult
			if err := db.First(&result, *row.BacktestResultID).Error; err != nil {
				return err
			}
			if result.ContentHash != row.BacktestResultContentHash {
				return fmt.Errorf("P10 評估點 %d 的回測引用不一致", saved.ID)
			}
		}
	}
	for _, saved := range backup.ResearchAnalyses {
		var row saasstore.ResearchAnalysisSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.PointSetHash != saved.PointSetHash {
			return fmt.Errorf("P10 分析快照 %d 還原後 hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.RobustCandidates {
		var row saasstore.RobustCandidate
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.CandidateKey != saved.CandidateKey || row.AdoptionUnitHash != saved.AdoptionUnitHash {
			return fmt.Errorf("P10 候選 %d 還原後身分不一致", saved.ID)
		}
	}
	for _, saved := range backup.ResearchComparisons {
		var row saasstore.ResearchComparisonSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.CommonManifestHash != saved.CommonManifestHash {
			return fmt.Errorf("P10 比較快照 %d 還原後 hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.SurrogateSnapshots {
		var row saasstore.SurrogateModelSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.SnapshotKey != saved.SnapshotKey || row.TrainingPointSetHash != saved.TrainingPointSetHash || row.ContentHash != saved.ContentHash {
			return fmt.Errorf("P10 代理模型 %d 還原後身分不一致", saved.ID)
		}
	}
	return nil
}

func verifyRestoredControlResearch(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.RandomParameterBatches {
		var row saasstore.RandomParameterBatch
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.BatchKey != saved.BatchKey || row.ParameterSpaceHash != saved.ParameterSpaceHash || row.FixedStructureHash != saved.FixedStructureHash || row.ContentHash != saved.ContentHash {
			return fmt.Errorf("P11 隨機參數批次 %d 還原後身分不一致", saved.ID)
		}
	}
	for _, saved := range backup.RandomParameterRecords {
		var row saasstore.RandomParameterRecord
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.BatchID != saved.BatchID || row.SequenceIndex != saved.SequenceIndex {
			return fmt.Errorf("P11 隨機參數紀錄 %d 還原後身分不一致", saved.ID)
		}
	}
	for _, saved := range backup.ControlAnalysisTasks {
		var row saasstore.ControlAnalysisTask
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.TaskKey != saved.TaskKey || row.CanonicalHash != saved.CanonicalHash || row.RandomBatchID != saved.RandomBatchID {
			return fmt.Errorf("P11 對照任務 %d 還原後身分不一致", saved.ID)
		}
		for _, reference := range []struct {
			id    *uint
			model any
			name  string
		}{{row.ComputeTaskID, &saasstore.ComputeTask{}, "計算任務"}, {row.LatestSnapshotID, &saasstore.ControlAnalysisSnapshot{}, "最新快照"}} {
			if reference.id == nil {
				continue
			}
			var count int64
			if err := db.Model(reference.model).Where("id = ?", *reference.id).Count(&count).Error; err != nil || count != 1 {
				return fmt.Errorf("P11 對照任務 %d 缺少%s", saved.ID, reference.name)
			}
		}
	}
	for _, saved := range backup.ControlEvaluations {
		var row saasstore.ControlEvaluation
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.SummaryHash != saved.SummaryHash || row.BacktestResultContentHash != saved.BacktestResultContentHash {
			return fmt.Errorf("P11 對照評估 %d 還原後 hash 不一致", saved.ID)
		}
		var result saasstore.BacktestResult
		if err := db.First(&result, row.BacktestResultID).Error; err != nil || result.ContentHash != row.BacktestResultContentHash {
			return fmt.Errorf("P11 對照評估 %d 的回測引用不一致", saved.ID)
		}
	}
	for _, saved := range backup.ControlSnapshots {
		var row saasstore.ControlAnalysisSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.SnapshotKey != saved.SnapshotKey || row.ContentHash != saved.ContentHash || row.StatisticsVersion != saved.StatisticsVersion {
			return fmt.Errorf("P11 對照快照 %d 還原後身分不一致", saved.ID)
		}
	}
	for _, saved := range backup.ControlSnapshotMembers {
		var row saasstore.ControlSnapshotMember
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.SnapshotID != saved.SnapshotID || row.EvaluationID != saved.EvaluationID || row.RepresentativeRole != saved.RepresentativeRole {
			return fmt.Errorf("P11 快照成員 %d 還原後不一致", saved.ID)
		}
	}
	return nil
}

func verifyRestoredDynamicModels(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.DynamicModelStudies {
		var study saasstore.DynamicModelStudy
		if err := db.First(&study, saved.ID).Error; err != nil {
			return err
		}
		if study.StudyKey != saved.StudyKey || study.SettingHash != saved.SettingHash || study.DatasetHash != saved.DatasetHash || study.ArtifactSetHash != saved.ArtifactSetHash {
			return fmt.Errorf("P09 研究 %d 還原後身分不一致", saved.ID)
		}
		for _, taskID := range []*uint{study.ComputeTaskID, study.MaterializationTaskID} {
			if taskID != nil {
				var count int64
				if err := db.Model(&saasstore.ComputeTask{}).Where("id = ?", *taskID).Count(&count).Error; err != nil || count != 1 {
					return fmt.Errorf("P09 研究 %d 缺少計算任務", saved.ID)
				}
			}
		}
	}
	for _, saved := range backup.DynamicModelArtifacts {
		var row saasstore.DynamicModelArtifact
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.DatasetHash != saved.DatasetHash {
			return fmt.Errorf("P09 模型 artifact %d hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.DynamicPredictions {
		var row saasstore.DynamicPredictionSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.BlockManifestHash != saved.BlockManifestHash {
			return fmt.Errorf("P09 預測快照 %d hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.DynamicPolicies {
		var row saasstore.DynamicPolicyArtifact
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.ParameterSpaceHash != saved.ParameterSpaceHash {
			return fmt.Errorf("P09 政策 %d hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.DynamicMaterializations {
		var row saasstore.DynamicMaterialization
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.BlockManifestHash != saved.BlockManifestHash {
			return fmt.Errorf("P09 物化結果 %d hash 不一致", saved.ID)
		}
		if row.BacktestResultID != nil {
			var result saasstore.BacktestResult
			if err := db.First(&result, *row.BacktestResultID).Error; err != nil {
				return err
			}
			if result.ContentHash != row.BacktestResultContentHash {
				return fmt.Errorf("P09 物化結果 %d 回測引用不一致", saved.ID)
			}
		}
	}
	for _, saved := range backup.DynamicReportSnapshots {
		var row saasstore.DynamicModelReportSnapshot
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.BlockManifestHash != saved.BlockManifestHash {
			return fmt.Errorf("P09 報告快照 %d hash 不一致", saved.ID)
		}
	}
	for _, saved := range backup.DynamicReportBlocks {
		var row saasstore.DynamicReportBlock
		if err := db.First(&row, saved.ID).Error; err != nil {
			return err
		}
		if row.ContentHash != saved.ContentHash || row.PointCount != saved.PointCount {
			return fmt.Errorf("P09 報告區塊 %d hash 不一致", saved.ID)
		}
	}
	return nil
}

func verifyRestoredRobustness(db *gorm.DB, backup incrementalBackup) error {
	for _, saved := range backup.RobustnessStudies {
		var study saasstore.RobustnessStudy
		if err := db.First(&study, saved.ID).Error; err != nil {
			return err
		}
		if study.StudyKey != saved.StudyKey || study.SettingHash != saved.SettingHash || study.SpaceHash != saved.SpaceHash {
			return fmt.Errorf("P08 研究 %d 還原後身分不一致", saved.ID)
		}
		if study.SourceGenomeID != nil {
			var count int64
			if err := db.Unscoped().Model(&saasstore.GeneRecord{}).Where("id = ?", *study.SourceGenomeID).Count(&count).Error; err != nil || count != 1 {
				return fmt.Errorf("P08 研究 %d 缺少來源參數", saved.ID)
			}
		}
		if study.ComputeTaskID != nil {
			var count int64
			if err := db.Model(&saasstore.ComputeTask{}).Where("id = ?", *study.ComputeTaskID).Count(&count).Error; err != nil || count != 1 {
				return fmt.Errorf("P08 研究 %d 缺少計算任務", saved.ID)
			}
		}
	}
	for _, saved := range backup.RobustnessPoints {
		var point saasstore.RobustnessEvaluationPoint
		if err := db.First(&point, saved.ID).Error; err != nil {
			return err
		}
		if point.CoordinateHash != saved.CoordinateHash || point.ParameterHash != saved.ParameterHash || point.MetricsHash != saved.MetricsHash {
			return fmt.Errorf("P08 評估點 %d 還原後 hash 不一致", saved.ID)
		}
		if point.BacktestResultID != nil {
			var result saasstore.BacktestResult
			if err := db.First(&result, *point.BacktestResultID).Error; err != nil {
				return err
			}
			if result.ContentHash != point.BacktestResultContentHash {
				return fmt.Errorf("P08 評估點 %d 的回測引用不一致", saved.ID)
			}
		}
	}
	for _, saved := range backup.RobustnessSnapshots {
		var snapshot saasstore.RobustnessAnalysisSnapshot
		if err := db.First(&snapshot, saved.ID).Error; err != nil {
			return err
		}
		if snapshot.ContentHash != saved.ContentHash || snapshot.PointSetHash != saved.PointSetHash {
			return fmt.Errorf("P08 分析快照 %d 還原後 hash 不一致", saved.ID)
		}
	}
	return nil
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

func changedSinceAny(db *gorm.DB, since time.Time, out any) error {
	return db.Where("created_at >= ? OR updated_at >= ?", since, since).Find(out).Error
}

func createdSinceAny(db *gorm.DB, since time.Time, out any) error {
	return db.Where("created_at >= ?", since).Find(out).Error
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
		"k_lines":                                 "id",
		"dataset_metadata":                        "id",
		"daily_execution_snapshots":               "id",
		"gene_records":                            "id",
		"gene_observations":                       "id",
		"evolution_tasks":                         "id",
		"backtest_specs":                          "id",
		"backtest_results":                        "id",
		"backtest_result_summaries":               "id",
		"backtest_path_blocks":                    "id",
		"backtest_runs":                           "id",
		"performance_reports":                     "id",
		"performance_report_summaries":            "id",
		"performance_report_chart_blocks":         "id",
		"compute_tasks":                           "id",
		"compute_cache_entries":                   "id",
		"compute_task_items":                      "id",
		"compute_task_dependencies":               "id",
		"robustness_studies":                      "id",
		"robustness_evaluation_points":            "id",
		"robustness_analysis_snapshots":           "id",
		"dynamic_model_studies":                   "id",
		"dynamic_model_artifacts":                 "id",
		"dynamic_prediction_snapshots":            "id",
		"dynamic_policy_artifacts":                "id",
		"dynamic_materializations":                "id",
		"dynamic_model_report_snapshots":          "id",
		"dynamic_report_blocks":                   "id",
		"market_series":                           "id",
		"market_data_versions":                    "id",
		"market_data_version_bars":                "id",
		"market_data_version_sources":             "id",
		"recomposition_plans":                     "id",
		"recomposition_plan_segments":             "id",
		"recomposition_preview_bars":              "id",
		"recomposition_generations":               "id",
		"recomposition_segment_instances":         "id",
		"recomposition_bar_lineages":              "id",
		"research_datasets":                       "id",
		"research_dataset_series":                 "id",
		"research_configurations":                 "id",
		"research_configuration_metadata":         "id",
		"research_runs":                           "id",
		"research_stages":                         "id",
		"research_evaluation_points":              "id",
		"research_point_origins":                  "id",
		"research_analysis_snapshots":             "id",
		"robust_regions":                          "id",
		"robust_region_points":                    "id",
		"robust_candidates":                       "id",
		"candidate_analysis_links":                "id",
		"candidate_gene_links":                    "id",
		"parameter_research_series":               "id",
		"parameter_research_series_members":       "id",
		"parameter_research_comparison_snapshots": "id",
		"surrogate_model_snapshots":               "id",
		"surrogate_proposals":                     "id",
		"random_parameter_batches":                "id",
		"random_parameter_records":                "id",
		"control_analysis_tasks":                  "id",
		"control_evaluations":                     "id",
		"control_analysis_snapshots":              "id",
		"control_snapshot_members":                "id",
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
