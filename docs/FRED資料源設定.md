# FRED 資料源設定

本文件記錄 QuantSaaS 如何使用 FRED API 匯入總經指標。FRED 屬於研究資料來源，不是交易所資料來源，也不涉及交易 API。

## API Key

FRED API Key 不得寫入 GitHub、前端程式、資料庫或測試 fixture。

本機使用方式：

1. 執行根目錄 `設定FRED_API_KEY.bat`。
2. 貼上 FRED API Key。
3. 重新啟動軟體，或在程式碼更新後執行 `重新建置並啟動.bat`。

Docker Compose 會把 Windows 環境變數 `FRED_API_KEY` 透傳給 SaaS 後端容器。

## 目前內建序列

目前內建三個 FRED 序列：

- `UNRATE`：美國失業率。
- `SOFR`：SOFR 擔保隔夜融資利率。
- `BAMLH0A0HYM2`：美國高收益債信用利差。

也可以在行情資料頁新增其他 FRED series id。資料來源選 `FRED`，週期目前固定為日資料。

## 儲存方式

FRED 回傳的是日期觀測值，不是 OHLC K 線。為了讓既有研究資料集、對齊、參數搜尋前檢查能共用同一套資料管線，系統會將每個觀測值存為一筆 `1d` 序列：

- `open = value`
- `high = value`
- `low = value`
- `close = value`
- `volume = 0`

這是資料相容層，不代表 FRED 指標真的有開高低收。

## 限制

- FRED 目前只支援 `1d` 匯入與自動更新。
- 不支援秒、分鐘、小時、週、月 K 匯入。
- 低頻資料，例如月資料，仍以實際公布日期存成日期序列。研究資料集會依照使用者選擇的缺值策略對齊主商品資料。
- FRED、NY Fed、ICE/BofA 等來源可能有引用或商業使用條款；若未來要商業化，需另外確認各序列授權。

