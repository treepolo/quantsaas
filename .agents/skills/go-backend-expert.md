# Go 後端專家技能

使用時機：實作 Go API、GORM model、JWT、Redis、WebSocket、cron、測試與後端封裝。

## 工作方式

1. 先閱讀 `CLAUDE.md` 與相關真源文件。
2. GORM model 是 schema 真源；修改資料表先改 struct tag。
3. Handler 保持薄層，業務規則下沉到 service。
4. 交易命令、成交回報、審計日誌必須可追溯且具備冪等鍵。
5. 測試優先覆蓋純函數、狀態轉換與安全邊界。

## 禁止事項

- 不建立 SQL migration。
- 不在 SaaS 側儲存交易所 API Key。
- 不把策略包反向依賴 SaaS service。
