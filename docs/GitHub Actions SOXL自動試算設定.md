# GitHub Actions SOXL 自動試算設定

這份文件對應備援方案第二層：GitHub Actions 每天自動跑輕量 SOXL #21 試算，不啟動完整 SaaS、不啟動資料庫、不需要交易 API Key。

## 它每天做什麼

排程工作位於：

```text
.github/workflows/soxl-emergency-signal.yml
```

每天美股收盤後，GitHub Actions 會：

1. 從 GitHub 下載專案。
2. 用 `EMERGENCY_BUNDLE_PASSWORD` 解開加密備援資料包。
3. 從 Yahoo 抓 SOXL 最新日線，並只更新緊急資料包。
4. 用 `cmd/emergency-signal latest` 計算 SOXL #21 最新基準模型目標權重。
5. 重新加密更新後的資料包。
6. 把最新結果與加密資料包提交回 GitHub。

## 需要先準備的東西

### 一、GitHub 上要有加密資料包

本機主系統正常時，先在 `D:\量化交易` 雙擊：

```text
匯出SOXL21加密備份到GitHub.bat
```

或：

```text
上傳SOXL21加密資料包到GitHub.bat
```

這會產生並推送：

```text
secure-backups/emergency/soxl-21.bundle.json.enc
```

如果你曾經手動輸入過收盤價，也會推送：

```text
secure-backups/emergency/soxl-21-manual-prices.jsonl.enc
```

### 二、GitHub Actions 要設定解密密碼

到 GitHub 專案頁面：

```text
Settings -> Secrets and variables -> Actions -> New repository secret
```

新增：

```text
Name: EMERGENCY_BUNDLE_PASSWORD
Value: 你匯出加密資料包時輸入的同一組密碼
```

沒有這個 Secret，Actions 會故意失敗，避免產生假結果。

## 要去哪裡看結果

Actions 成功後，GitHub 會更新：

```text
secure-backups/emergency/soxl-21-latest.md
secure-backups/emergency/soxl-21-latest.json
```

平常你看 `.md` 就夠了；裡面會有：

- 最新日期
- 最新收盤價
- 市場狀態
- 基準模型目標權重
- 前一筆基準模型目標權重
- 基準模型目標權重變化
- 模型淨值與日變化

`.json` 是給之後手機頁、其他小工具或自動通知使用。

## 手動執行

如果你想立刻跑一次：

1. 到 GitHub 專案頁。
2. 點 `Actions`。
3. 點 `SOXL 緊急每日試算`。
4. 點 `Run workflow`。

成功後，頁面摘要會直接顯示最新試算結果，也會更新 `secure-backups/emergency/soxl-21-latest.md`。

## 失敗時怎麼看

常見失敗原因：

| 錯誤 | 意思 | 處理方式 |
|---|---|---|
| `Missing repository secret: EMERGENCY_BUNDLE_PASSWORD` | 還沒設定 GitHub Secret | 按本文設定 Secret |
| `Missing secure-backups/emergency/soxl-21.bundle.json.enc` | GitHub 上還沒有加密資料包 | 在本機執行匯出/上傳 BAT |
| `yahoo chart status ...` | Yahoo 當下拒絕或資料源異常 | 稍後手動重跑；必要時改用第一層手動收盤價 |
| 沒有新日期 | 美股休市、資料尚未更新，或 Yahoo 還沒吐出最新日線 | 先看最新日期是否仍合理 |

## 安全邊界

這個 workflow 不會：

- 啟動完整 SaaS。
- 連 PostgreSQL。
- 連 Redis。
- 使用交易 API Key。
- 把明文 `soxl-21.bundle.json` 提交到 GitHub。

GitHub 上會保存加密資料包與最新試算結果。若 repo 是公開的，最新試算結果也會被公開；目前建議 repo 維持 private。
