package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quantsaas/internal/saas/config"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const backupVersion = 1

type incrementalBackup struct {
	Version             int                                `json:"version"`
	Kind                string                             `json:"kind"`
	CreatedAt           string                             `json:"created_at"`
	Since               string                             `json:"since"`
	ResearchInstruments []saasstore.ResearchInstrument     `json:"research_instruments"`
	KLines              []saasstore.KLine                  `json:"k_lines"`
	DatasetMetadata     []saasstore.DatasetMetadata        `json:"dataset_metadata"`
	DailySnapshots      []saasstore.DailyExecutionSnapshot `json:"daily_execution_snapshots"`
	GeneRecords         []saasstore.GeneRecord             `json:"gene_records"`
	GeneObservations    []saasstore.GeneObservation        `json:"gene_observations"`
	EvolutionTasks      []saasstore.EvolutionTask          `json:"evolution_tasks"`
	BacktestRuns        []saasstore.BacktestRun            `json:"backtest_runs"`
	Counts              map[string]int                     `json:"counts"`
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
	if backup.Version != backupVersion || backup.Kind != "incremental" {
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
	if err := changedSince(db, since, &backup.BacktestRuns); err != nil {
		return backup, err
	}
	backup.Counts["research_instruments"] = len(backup.ResearchInstruments)
	backup.Counts["k_lines"] = len(backup.KLines)
	backup.Counts["dataset_metadata"] = len(backup.DatasetMetadata)
	backup.Counts["daily_execution_snapshots"] = len(backup.DailySnapshots)
	backup.Counts["gene_records"] = len(backup.GeneRecords)
	backup.Counts["gene_observations"] = len(backup.GeneObservations)
	backup.Counts["evolution_tasks"] = len(backup.EvolutionTasks)
	backup.Counts["backtest_runs"] = len(backup.BacktestRuns)
	return backup, nil
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
		if err := saveAll(tx, backup.BacktestRuns); err != nil {
			return err
		}
		return resetSequences(tx)
	})
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
		if err := db.Save(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func saveAllUnscoped[T any](db *gorm.DB, rows []T) error {
	for i := range rows {
		if err := db.Unscoped().Save(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetSequences(db *gorm.DB) error {
	tables := map[string]string{
		"k_lines":                   "id",
		"dataset_metadata":          "id",
		"daily_execution_snapshots": "id",
		"gene_records":              "id",
		"gene_observations":         "id",
		"evolution_tasks":           "id",
		"backtest_runs":             "id",
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

環境：
  需要 --dsn 或 DATABASE_DSN。此工具只做資料匯出/匯入，不啟動交易功能。`)
	return nil
}
