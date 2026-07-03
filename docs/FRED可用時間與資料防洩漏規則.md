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

- `observation_date`：FRED 回傳的 `date`，存入 K 線 `open_time`，代表該筆資料描述的經濟期間。
- `realtime_start`：該觀測值第一次出現有效值的 vintage date，存入 `k_line_observation_metadata.realtime_start_ms`。
- `realtime_end`：目前與 `realtime_start` 相同，存入 `k_line_observation_metadata.realtime_end_ms`。
- `available_at`：目前固定為 `realtime_start + 1 day`，存入 `k_line_observation_metadata.available_at_ms`。

## 對齊規則

研究資料集不得只用 `observation_date` 對齊 FRED 指標。

參考指標只有在以下條件成立時，才可被視為主商品當根資料可用：

```text
available_at <= 主商品該根資料時間
```

各缺值策略也必須遵守此規則：

- `empty`：只接受同一時間且已可用的資料。
- `forward_fill`：只延續已可用的最近一筆資料。
- `linear`：只有前後兩筆資料都已在該時間點可用時，才允許插值。

## 清資料規則

若 FRED 可用時間規則有重大變更，必須清除舊 FRED 匯入資料後重新匯入，避免沒有 `realtime_start` / `available_at` 的舊資料混入研究資料集。
