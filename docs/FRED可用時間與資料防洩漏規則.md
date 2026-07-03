# FRED 可用時間與資料防洩漏規則

本文件記錄 FRED 資料匯入與研究資料集對齊時的時間語義，避免參數搜尋、回測或市場狀態計算使用尚未發布的資料。

## 匯入規則

FRED 官方文件列出 `output_type=4` 可代表初次發布版本，但目前實測內建序列會回 HTTP 400。因此系統不直接使用 `output_type=4`。

目前匯入流程：

1. 使用 `fred/series/vintagedates` 取得該序列的 vintage date 清單。
2. 分批呼叫 `fred/series/observations`，並帶入：

- `file_type=json`
- `sort_order=asc`
- `output_type=3`
- `vintage_dates=<分批 vintage date 清單>`

3. 對每個 `observation_date`，取第一次出現有效值的 vintage date，視為初版發布日。

匯入時，系統會保存以下時間：

- `observation_date`：FRED 回傳的 `date`，存入 `k_line_observation_metadata.observation_time_ms`，代表該筆資料描述的經濟期間。
- `realtime_start`：該觀測值第一次出現有效值的 vintage date，存入 `k_line_observation_metadata.realtime_start_ms`。
- `realtime_end`：目前與 `realtime_start` 相同，存入 `k_line_observation_metadata.realtime_end_ms`。
- `open_time`：存入該筆資料的初版發布日期，也就是 `realtime_start`。
- `available_at`：預設等於 `realtime_start`，存入 `k_line_observation_metadata.available_at_ms`。

若同一個 FRED 序列在同一個發布日期帶出多個觀測期間，系統只保留該發布日期中觀測期間最新的值，作為該發布日期可用的指標值。

## 對齊規則

研究資料集不得使用 `observation_date` 對齊 FRED 指標。對齊主時間必須使用 `open_time`，也就是初版發布日期。

參考指標只有在以下條件成立時，才可被視為主商品當根資料可用：

```text
available_at <= 主商品該根資料時間
```

缺值策略固定為延續前值，且必須遵守此規則：

- `forward_fill`：只延續已可用的最近一筆資料。

研究資料集可選擇啟用「發布日期延後可用」。若啟用，參考指標的可用時間為：

```text
available_at = open_time + n 天
```

若未啟用，參考指標在發布日期當日即視為可用。

## 清資料規則

若 FRED 可用時間規則有重大變更，必須清除舊 FRED 匯入資料後重新匯入，避免舊版 `observation_date` 作為 `open_time` 的資料混入研究資料集。
