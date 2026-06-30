# QuantSaaS Agent 指引

本專案以 `docs/` 三份真源文件作為唯一需求來源。所有代理工作需遵守 `CLAUDE.md`，並保持輸出為正體中文。

## 代理工作規則

1. 先讀文件，再改程式。
2. 不把交易所憑證帶入 SaaS、資料庫、前端或測試 fixture。
3. 不在策略核心內新增任何 I/O、網路、時間或資料庫依賴。
4. 不建立 SQL migration；資料表結構只由 GORM model 與 AutoMigrate 管理。
5. 不在使用者可見 UI 中暴露 `DeadBTC`、`FloatBTC`、`Step()`、`VolatilityRatio`、`TheoreticalUSD`、策略內部代號等術語。
6. 若文件與程式衝突，文件優先；若文件缺漏，先回報或補文件。
7. 終端機、Docker、測試、格式化、重啟服務等操作，優先使用 `scripts/dev/` 的固定入口；使用者日常啟動優先使用根目錄 `.bat`。細節見 `docs/開發操作手冊.md`。

## 固定開發入口

為避免 PowerShell 轉義、Docker config、UTF-8 亂碼、測試超時與重啟過慢等反覆問題，Agent 不應每次臨場拼長命令。

- 日常啟動：`啟動軟體.bat`
- 日常重啟：`重啟軟體.bat`
- 重新建置並啟動：`重新建置並啟動.bat`
- 查看服務狀態：`查看服務狀態.bat`
- Docker compose 腳本：`scripts/dev/compose-*.ps1`
- Go 測試與格式化：`scripts/dev/go-test.ps1`、`scripts/dev/go-fmt.ps1`
- 前端建置：`scripts/dev/frontend-build.ps1`

只有在程式碼或容器建置內容改動後，才使用 rebuild；一般啟動或重啟不得無故使用 `--build`。

## 建議技能路由

- 架構、模組邊界、生命週期：使用 `.agents/skills/system-architect.md`
- 策略數學、倉位、回測一致性：使用 `.agents/skills/quant-math-expert.md`
- Go 後端、GORM、API、測試：使用 `.agents/skills/go-backend-expert.md`
- Docker、設定、安全與部署：使用 `.agents/skills/deployment-ops-expert.md`
