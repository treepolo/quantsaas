# Oracle Cloud Always Free 部署筆記

這份部署設定是給個人研究用的雲端 Lab，不放交易所 API key，不開交易功能。雲端服務以 `APP_ROLE=lab` 啟動，提供行情資料、參數搜尋、回測、參數庫與市場狀態頁。

## 你需要先做的事

1. 註冊 Oracle Cloud Free Tier。
2. 建立一台 Always Free VM，建議 Ubuntu 24.04 / Ampere A1。
3. 建議規格：2 OCPU、12GB RAM，或至少 1 OCPU、6GB RAM。
4. 建議磁碟：50GB 以上。
5. 下載並保存 Oracle 給你的 SSH private key。

## VM 第一次設定

SSH 進 VM 後執行：

```bash
sudo apt-get update
sudo apt-get install -y git
git clone https://github.com/treepolo/quantsaas.git
cd quantsaas
bash deploy/oracle/setup_ubuntu.sh
```

登出再登入，讓 Docker 群組生效，然後啟動 Tailscale：

```bash
sudo tailscale up
```

依照畫面網址登入 Tailscale。

## 建立雲端環境變數

```bash
cd ~/quantsaas
cp deploy/oracle/.env.cloud.example deploy/oracle/.env.cloud
nano deploy/oracle/.env.cloud
```

至少要改：

```text
POSTGRES_PASSWORD=一組長密碼
JWT_SECRET=一組很長的隨機字串
```

可以用這個產生隨機字串：

```bash
openssl rand -base64 48
```

## 啟動服務

```bash
cd ~/quantsaas
docker compose --env-file deploy/oracle/.env.cloud -f deploy/oracle/docker-compose.oracle.yml up -d --build
```

檢查：

```bash
docker compose --env-file deploy/oracle/.env.cloud -f deploy/oracle/docker-compose.oracle.yml ps
curl http://127.0.0.1:8080/api/v1/system/status
```

## 只透過 Tailscale 開放網站

服務只綁在 VM 的 `127.0.0.1:8080`，不直接公開到網際網路。用 Tailscale Serve 發給你的私人網路：

```bash
sudo tailscale serve --bg http://127.0.0.1:8080
```

之後手機或筆電登入同一個 Tailscale 帳號，就能用 Tailscale 顯示的 HTTPS 網址打開。

## 備份資料庫

手動備份：

```bash
cd ~/quantsaas
bash deploy/oracle/backup_db.sh
```

設定每天自動備份：

```bash
crontab -e
```

加入：

```cron
15 20 * * * cd /home/ubuntu/quantsaas && bash deploy/oracle/backup_db.sh >> backups/backup.log 2>&1
```

Oracle VM 使用 UTC，`20:15 UTC` 約等於台灣時間隔天 `04:15`。

## 從備份還原

```bash
cd ~/quantsaas
bash deploy/oracle/restore_db.sh backups/quantsaas-YYYYMMDD-HHMMSS.sql.gz
```

## 更新程式

```bash
cd ~/quantsaas
git pull
docker compose --env-file deploy/oracle/.env.cloud -f deploy/oracle/docker-compose.oracle.yml up -d --build
```

## 從本機搬資料到雲端的大方向

1. 在本機 PowerShell 執行 `.\deploy\oracle\export_local_db.ps1` 匯出資料庫。
2. 用 `scp` 把 `.sql` 傳到 VM 的 `~/quantsaas/backups/`。
3. 在 VM 執行 `restore_db.sh`。

這一步我可以等 VM 建好後幫你做。
