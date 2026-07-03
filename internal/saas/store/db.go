package store

import (
	"database/sql"
	"fmt"
	"time"

	"quantsaas/internal/saas/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	*gorm.DB
}

func NewDB(cfg config.DatabaseConfig) (*DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("database DSN is required")
	}

	gdb, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("unwrap sql db: %w", err)
	}
	applyPoolConfig(sqlDB, cfg)

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
		&BacktestRun{},
		&ResearchInstrument{},
		&KLine{},
		&KLineObservationMetadata{},
		&DatasetMetadata{},
		&ResearchDataset{},
		&ResearchDatasetSeries{},
		&DailyExecutionSnapshot{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	return &DB{DB: gdb}, nil
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
