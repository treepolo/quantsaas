package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/emergency"
	"quantsaas/internal/saas/marketdata"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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
	case "export":
		return runExport(args[1:])
	case "calc":
		return runCalc(args[1:])
	case "latest":
		return runLatest(args[1:])
	case "refresh-yahoo":
		return runRefreshYahoo(args[1:])
	case "encrypt":
		return runEncrypt(args[1:])
	case "decrypt":
		return runDecrypt(args[1:])
	case "help", "-h", "--help":
		return usage()
	default:
		return fmt.Errorf("未知指令: %s", args[0])
	}
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	parameterID := fs.Uint("parameter-id", 21, "參數 ID")
	out := fs.String("out", "emergency/soxl-21.bundle.json", "輸出資料包路徑")
	dsn := fs.String("dsn", "", "PostgreSQL DSN；未填時讀 DATABASE_DSN 或 config")
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
	bundle, err := emergency.ExportBundle(context.Background(), db, emergency.ExportRequest{ParameterID: uint(*parameterID)})
	if err != nil {
		return err
	}
	if err := ensureParentDir(*out); err != nil {
		return err
	}
	if err := emergency.SaveBundle(*out, bundle); err != nil {
		return err
	}
	fmt.Printf("已匯出備援資料包：%s\n", *out)
	fmt.Printf("標的：%s / 週期：%s / 資料筆數：%d\n", bundle.InstrumentID, bundle.Interval, len(bundle.Bars))
	return nil
}

func runCalc(args []string) error {
	fs := flag.NewFlagSet("calc", flag.ContinueOnError)
	bundlePath := fs.String("bundle", "emergency/soxl-21.bundle.json", "備援資料包路徑")
	manualPath := fs.String("manual-file", "emergency/soxl-21-manual-prices.jsonl", "手動收盤價 JSONL")
	date := fs.String("date", "", "收盤日期，例如 2026-06-22")
	closeValue := fs.Float64("close", 0, "收盤價")
	outJSON := fs.String("out-json", "emergency/soxl-21-latest.json", "最新結果 JSON")
	outMD := fs.String("out-md", "emergency/soxl-21-latest.md", "最新結果 Markdown")
	jsonOnly := fs.Bool("json", false, "只輸出 JSON 到 stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*date) == "" || *closeValue <= 0 {
		return fmt.Errorf("calc 需要 --date 與正數 --close")
	}
	bundle, err := emergency.LoadBundle(*bundlePath)
	if err != nil {
		return err
	}
	if err := ensureParentDir(*manualPath); err != nil {
		return err
	}
	if err := emergency.AppendManualPrice(*manualPath, emergency.ManualPrice{Date: *date, Close: *closeValue}); err != nil {
		return err
	}
	return calculateAndWrite(bundle, *manualPath, *outJSON, *outMD, *jsonOnly)
}

func runLatest(args []string) error {
	fs := flag.NewFlagSet("latest", flag.ContinueOnError)
	bundlePath := fs.String("bundle", "emergency/soxl-21.bundle.json", "備援資料包路徑")
	manualPath := fs.String("manual-file", "emergency/soxl-21-manual-prices.jsonl", "手動收盤價 JSONL")
	outJSON := fs.String("out-json", "emergency/soxl-21-latest.json", "最新結果 JSON")
	outMD := fs.String("out-md", "emergency/soxl-21-latest.md", "最新結果 Markdown")
	jsonOnly := fs.Bool("json", false, "只輸出 JSON 到 stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bundle, err := emergency.LoadBundle(*bundlePath)
	if err != nil {
		return err
	}
	return calculateAndWrite(bundle, *manualPath, *outJSON, *outMD, *jsonOnly)
}

func runRefreshYahoo(args []string) error {
	fs := flag.NewFlagSet("refresh-yahoo", flag.ContinueOnError)
	bundlePath := fs.String("bundle", "emergency/soxl-21.bundle.json", "要更新的緊急資料包")
	outPath := fs.String("out", "", "更新後輸出路徑，留空時覆蓋 --bundle")
	symbol := fs.String("symbol", "", "Yahoo 代碼，留空時使用資料包 symbol")
	interval := fs.String("interval", "", "Yahoo 週期，留空時使用資料包 interval")
	lookbackDays := fs.Int("lookback-days", 10, "從既有最後一筆往回重抓幾天，以修正晚到或還原價調整")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bundle, err := emergency.LoadBundle(*bundlePath)
	if err != nil {
		return err
	}
	resolvedSymbol := strings.TrimSpace(*symbol)
	if resolvedSymbol == "" {
		resolvedSymbol = bundle.Symbol
	}
	resolvedInterval := strings.TrimSpace(*interval)
	if resolvedInterval == "" {
		resolvedInterval = bundle.Interval
	}
	if resolvedSymbol == "" || resolvedInterval == "" {
		return fmt.Errorf("refresh-yahoo 需要 symbol 與 interval")
	}
	if resolvedInterval != "1d" {
		return fmt.Errorf("GitHub Actions 緊急試算目前只支援日線更新，收到 interval=%s", resolvedInterval)
	}
	out := strings.TrimSpace(*outPath)
	if out == "" {
		out = *bundlePath
	}

	latest := emergency.LatestBarOpenTimeMs(bundle.Bars)
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -30).UnixMilli()
	if latest > 0 {
		start = time.UnixMilli(latest).UTC().AddDate(0, 0, -*lookbackDays).UnixMilli()
	}
	end := now.AddDate(0, 0, 2).UnixMilli()

	client := marketdata.NewYahooClient(marketdata.DefaultYahooBaseURL)
	rows, err := client.FetchKLines(context.Background(), resolvedSymbol, resolvedInterval, start, end)
	if err != nil {
		return fmt.Errorf("抓取 Yahoo 最新資料失敗: %w", err)
	}
	incoming := make([]emergency.Bar, 0, len(rows))
	for _, row := range rows {
		if row.Close <= 0 {
			continue
		}
		incoming = append(incoming, emergency.NewBar(row.OpenTime, row.Open, row.High, row.Low, row.Close, row.Volume))
	}
	beforeCount := len(bundle.Bars)
	beforeLatest := emergency.LatestBarOpenTimeMs(bundle.Bars)
	bundle.Bars = emergency.MergeBars(bundle.Bars, incoming)
	afterLatest := emergency.LatestBarOpenTimeMs(bundle.Bars)
	if err := ensureParentDir(out); err != nil {
		return err
	}
	if err := emergency.SaveBundle(out, bundle); err != nil {
		return err
	}

	fmt.Printf("Yahoo 更新完成：抓到 %d 筆，資料包 %d -> %d 筆\n", len(incoming), beforeCount, len(bundle.Bars))
	if afterLatest > beforeLatest {
		fmt.Printf("最新日線：%s\n", time.UnixMilli(afterLatest).UTC().Format("2006-01-02"))
	} else if afterLatest > 0 {
		fmt.Printf("最新日線未變：%s\n", time.UnixMilli(afterLatest).UTC().Format("2006-01-02"))
	}
	return nil
}

func runEncrypt(args []string) error {
	fs := flag.NewFlagSet("encrypt", flag.ContinueOnError)
	inPath := fs.String("in", "emergency/soxl-21.bundle.json", "明文資料包路徑")
	outPath := fs.String("out", "secure-backups/emergency/soxl-21.bundle.json.enc", "加密備份輸出路徑")
	passphraseEnv := fs.String("passphrase-env", "EMERGENCY_BUNDLE_PASSWORD", "讀取密碼的環境變數")
	if err := fs.Parse(args); err != nil {
		return err
	}
	passphrase := os.Getenv(*passphraseEnv)
	if passphrase == "" {
		return fmt.Errorf("請先設定 %s", *passphraseEnv)
	}
	if err := ensureParentDir(*outPath); err != nil {
		return err
	}
	if err := emergency.EncryptFile(*inPath, *outPath, passphrase); err != nil {
		return err
	}
	fmt.Printf("已建立加密備份：%s\n", *outPath)
	return nil
}

func runDecrypt(args []string) error {
	fs := flag.NewFlagSet("decrypt", flag.ContinueOnError)
	inPath := fs.String("in", "secure-backups/emergency/soxl-21.bundle.json.enc", "加密備份路徑")
	outPath := fs.String("out", "emergency/soxl-21.bundle.json", "明文資料包輸出路徑")
	passphraseEnv := fs.String("passphrase-env", "EMERGENCY_BUNDLE_PASSWORD", "讀取密碼的環境變數")
	if err := fs.Parse(args); err != nil {
		return err
	}
	passphrase := os.Getenv(*passphraseEnv)
	if passphrase == "" {
		return fmt.Errorf("請先設定 %s", *passphraseEnv)
	}
	if err := ensureParentDir(*outPath); err != nil {
		return err
	}
	if err := emergency.DecryptFile(*inPath, *outPath, passphrase); err != nil {
		return err
	}
	fmt.Printf("已解密備援資料包：%s\n", *outPath)
	return nil
}

func calculateAndWrite(bundle emergency.Bundle, manualPath string, outJSON string, outMD string, jsonOnly bool) error {
	manual, err := emergency.LoadManualPrices(manualPath)
	if err != nil {
		return err
	}
	result, err := emergency.Calculate(bundle, manual)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if jsonOnly {
		fmt.Println(string(raw))
		return nil
	}
	if outJSON != "" {
		if err := ensureParentDir(outJSON); err != nil {
			return err
		}
		if err := os.WriteFile(outJSON, append(raw, '\n'), 0o600); err != nil {
			return err
		}
	}
	if outMD != "" {
		if err := ensureParentDir(outMD); err != nil {
			return err
		}
		if err := os.WriteFile(outMD, []byte(emergency.RenderMarkdown(result)), 0o600); err != nil {
			return err
		}
	}
	fmt.Print(emergency.RenderMarkdown(result))
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
	fmt.Println(`緊急試算工具

用法：
  emergency-signal export --parameter-id 21 --out emergency/soxl-21.bundle.json
  emergency-signal latest --bundle emergency/soxl-21.bundle.json
  emergency-signal calc --bundle emergency/soxl-21.bundle.json --date 2026-06-22 --close 28.34
  emergency-signal refresh-yahoo --bundle emergency/soxl-21.bundle.json --symbol SOXL
  emergency-signal encrypt --in emergency/soxl-21.bundle.json --out secure-backups/emergency/soxl-21.bundle.json.enc
  emergency-signal decrypt --in secure-backups/emergency/soxl-21.bundle.json.enc --out emergency/soxl-21.bundle.json

環境：
  export 需要 --dsn 或 DATABASE_DSN。latest/calc 不需要資料庫。
  encrypt/decrypt 需要 EMERGENCY_BUNDLE_PASSWORD。`)
	return nil
}
