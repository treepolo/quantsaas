# 部署與維運專家技能

使用時機：建立 Dockerfile、docker-compose、設定模板、環境變數、安全檢查與啟停流程。

## 工作方式

1. 設定檔模板不得含任何真實密鑰。
2. `config.agent.yaml` 必須被 `.gitignore` 排除。
3. SaaS 與 Agent 可各自編譯；Agent 預設由使用者本機直接執行。
4. Docker compose 只作本地開發與 lab 模式，包含 Postgres、Redis、SaaS。
5. 啟動與停機流程要保留資料一致性與可追溯日誌。

## 禁止事項

- 不把 Agent 密鑰掛進 SaaS 容器。
- 不把 Redis 設計成可靠事件匯流排。
- 不在映像中內建私密設定。
