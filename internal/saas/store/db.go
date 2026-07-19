package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"quantsaas/internal/saas/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type DB struct {
	*gorm.DB
}

func NewDB(cfg config.DatabaseConfig) (*DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("database DSN is required")
	}

	// Backtest path blocks contain large JSON payloads. GORM's trace callback
	// can interpolate query values before writing slow-query and error logs for
	// some dialect paths. Disable SQL trace so path payloads cannot inflate
	// Docker/WSL log caches; services still return operation errors normally.
	dbLogger := gormlogger.Default.LogMode(gormlogger.Silent)
	gdb, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{Logger: dbLogger})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("unwrap sql db: %w", err)
	}
	applyPoolConfig(sqlDB, cfg)

	if err := AutoMigrate(gdb); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	return &DB{DB: gdb}, nil
}

// AutoMigrate keeps the GORM model list as the only schema source for the
// service and maintenance tools.
func AutoMigrate(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(
		&User{},
		&StrategyTemplate{},
		&StrategyInstance{},
		&PortfolioState{},
		&RuntimeState{},
		&SpotLot{},
		&TradeRecord{},
		&SpotExecution{},
		&AuditLog{},
		&GeneRecord{},
		&GeneObservation{},
		&EvolutionTask{},
		&BacktestSpec{},
		&BacktestResult{},
		&BacktestResultSummary{},
		&BacktestPathBlock{},
		&PerformanceReport{},
		&PerformanceReportSummary{},
		&PerformanceReportChartBlock{},
		&ComputeTask{},
		&ComputeTaskDependency{},
		&ComputeCacheEntry{},
		&RobustnessStudy{},
		&RobustnessEvaluationPoint{},
		&RobustnessAnalysisSnapshot{},
		&DynamicModelStudy{},
		&DynamicModelArtifact{},
		&DynamicPredictionSnapshot{},
		&DynamicPolicyArtifact{},
		&DynamicMaterialization{},
		&DynamicModelReportSnapshot{},
		&DynamicReportBlock{},
		&ResearchConfiguration{},
		&ResearchConfigurationMetadata{},
		&ResearchRun{},
		&ResearchStage{},
		&ResearchEvaluationPoint{},
		&ResearchPointOrigin{},
		&ResearchAnalysisSnapshot{},
		&RobustRegion{},
		&RobustRegionPoint{},
		&RobustCandidate{},
		&CandidateAnalysisLink{},
		&CandidateGeneLink{},
		&ResearchSeries{},
		&ResearchSeriesMember{},
		&ResearchComparisonSnapshot{},
		&SurrogateModelSnapshot{},
		&SurrogateProposal{},
		&RandomParameterBatch{},
		&RandomParameterRecord{},
		&ControlAnalysisTask{},
		&ControlEvaluation{},
		&ControlAnalysisSnapshot{},
		&ControlSnapshotMember{},
		&KlineInverseStudy{},
		&KlineInverseCalibration{},
		&KlineInverseBatch{},
		&KlineInversePath{},
		&KlineInverseEvaluation{},
		&KlineInverseLineageEdge{},
		&KlineInverseArchiveSnapshot{},
		&KlineInverseProbeBatch{},
		&KlineInverseSourceLink{},
		&ComputeTaskItem{},
		&BacktestRun{},
		&ResearchInstrument{},
		&KLine{},
		&KLineObservationMetadata{},
		&DatasetMetadata{},
		&MarketSeries{},
		&MarketDataVersion{},
		&MarketDataVersionBar{},
		&MarketDataVersionSource{},
		&RecompositionPlan{},
		&RecompositionPlanSegment{},
		&RecompositionPreviewBar{},
		&RecompositionGeneration{},
		&RecompositionSegmentInstance{},
		&RecompositionBarLineage{},
		&PerturbationSourceSnapshot{},
		&PerturbationGroup{},
		&PerturbationVariant{},
		&PerturbationTest{},
		&PerturbationTestSubject{},
		&PerturbationTestBatch{},
		&PerturbationTestRun{},
		&PerturbationAnalysisSnapshot{},
		&PerturbationMetricSummary{},
		&PerturbationQualificationSummary{},
		&ResearchDataset{},
		&ResearchDatasetSeries{},
		&DailyExecutionSnapshot{},
	); err != nil {
		return err
	}
	return repairResearchSeriesOwnerKey(gdb)
}

// GORM does not replace an existing index when its name is unchanged but its
// columns change. P10 originally created idx_research_series_owner_key on
// series_key alone; keep the model as the schema source and repair that legacy
// AutoMigrate result through GORM's migrator.
func repairResearchSeriesOwnerKey(gdb *gorm.DB) error {
	const indexName = "idx_research_series_owner_key"
	indexes, err := gdb.Migrator().GetIndexes(&ResearchSeries{})
	if err != nil {
		return fmt.Errorf("inspect %s: %w", indexName, err)
	}
	indexExists := false
	for _, index := range indexes {
		if index.Name() != indexName {
			continue
		}
		indexExists = true
		unique, _ := index.Unique()
		columns := append([]string(nil), index.Columns()...)
		sort.Strings(columns)
		if unique && len(columns) == 2 && columns[0] == "owner_user_id" && columns[1] == "series_key" {
			return nil
		}
		break
	}
	return gdb.Transaction(func(tx *gorm.DB) error {
		if indexExists {
			if err := tx.Migrator().DropIndex(&ResearchSeries{}, indexName); err != nil {
				return fmt.Errorf("drop legacy %s: %w", indexName, err)
			}
		}
		if err := tx.Migrator().CreateIndex(&ResearchSeries{}, indexName); err != nil {
			return fmt.Errorf("create composite %s: %w", indexName, err)
		}
		return nil
	})
}

func applyPoolConfig(db *sql.DB, cfg config.DatabaseConfig) {
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	db.SetConnMaxLifetime(30 * time.Minute)
}
