# QuantSaaS 專案協作憲法

## 唯一功能真源

目前專案的功能定義只依據 `docs/` 下三份真源文件：

- `docs/系統總體拓撲結構.md`
- `docs/策略數學引擎.md`
- `docs/進化計算引擎.md`

三份文件沒有定義的功能，不進入實作。若需求看似合理但未出現在真源文件中，先補文件或回報缺口，不直接寫入程式。

## 工作順序

1. 涉及策略、回測、GA、染色體、倉位語義時，先閱讀對應真源文件，再改程式。
2. 涉及 Go 後端與資料庫時，遵守 GORM Code-First，只使用 Go struct 與 AutoMigrate，不建立 SQL migration。
3. 涉及價格、收益、波動、訊號時，優先使用無量綱表示，例如比率、對數收益率、標準化偏離，不跨標的比較絕對價格。
4. 涉及架構邊界時，維持 SaaS、Strategy、Agent 分工，不做預防性解耦，也不讓 Agent 取得策略邏輯。

## 核心約束

1. 策略必須能論證複利如何發生；不能形成權益正回饋滾動的策略，不進入實作。
2. 回測與實盤必須呼叫同一個 `Step()` 實作；`Step()` 內禁止 `if isBacktest` 之類分支。
3. `Step()` 只在 SaaS 側執行；Agent 不含任何策略程式碼。
4. 策略包內部禁止網路請求、資料庫讀寫、檔案 I/O、計時器與不可控外部狀態。
5. 交易所 API Key 只能存在於 `config.agent.yaml`，不得進入 SaaS、資料庫、日誌或前端。
6. 價格相關計算必須無量綱化，避免用絕對價格直接比較不同標的。

## 程式目錄職責

- `cmd/saas/`：SaaS 服務入口，負責載入設定、初始化 DB/Redis、HTTP 路由、WebSocket Hub、cron tick。
- `cmd/agent/`：LocalAgent 入口，負責讀取本地 `config.agent.yaml`、登入 SaaS、維持 WebSocket、執行交易所下單。
- `internal/saas/`：SaaS 後端實作，包含設定、資料庫、認證、實例管理、進化任務、WebSocket 與 API handler。
- `internal/agent/`：LocalAgent 執行端實作，包含交易所 adapter、本地設定、重連、命令執行與上報。
- `internal/strategy/`：策略抽象契約與註冊表，定義 `Step()` 介面、模板 metadata 與策略輸入輸出約束。
- `internal/strategies/[策略名]/`：具體策略純函數核心，只讀 `StrategyInput`，只回傳 `StrategyOutput`。
- `internal/quant/`：共用量化數學工具、資料結構、資產三態、Ghost DCA、Sigmoid 微觀引擎。
- `internal/adapters/backtest/`：回測 adapter，負責把歷史 K 線與資產快照轉成與實盤一致的 `StrategyInput`。

## 驗證命令

```powershell
go list ./...
go test ./...
```

若本機尚未安裝 Go，先不要假裝驗證通過；明確標註工具鏈缺失。
