. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root

$rclone = Get-RclonePath -Root $root
if ([string]::IsNullOrWhiteSpace($rclone)) {
    throw "找不到 rclone。請確認 .tools\rclone\rclone.exe 存在。"
}

Write-Host ""
Write-Host "接下來會開啟 rclone 設定。請照下面選："
Write-Host ""
Write-Host "1. 選 n：New remote"
Write-Host "2. name 填：gdrive"
Write-Host "3. Storage 選 Google Drive / drive"
Write-Host "4. client_id 留空，直接 Enter"
Write-Host "5. client_secret 留空，直接 Enter"
Write-Host "6. scope 選 1"
Write-Host "7. service_account_file 留空"
Write-Host "8. Edit advanced config 選 n"
Write-Host "9. Use auto config 選 y，瀏覽器登入 Google 授權"
Write-Host "10. Configure this as a Shared Drive 選 n"
Write-Host "11. 確認設定選 y"
Write-Host "12. 最後選 q 離開"
Write-Host ""
Write-Host "如果你已經有名為 gdrive 的 remote，也可以直接 q 離開。"
Write-Host ""
Pause

& $rclone config
if ($LASTEXITCODE -ne 0) {
    throw "rclone config failed, exit code: $LASTEXITCODE"
}

Write-Host ""
Write-Host "檢查 gdrive 連線..."
& $rclone lsd "gdrive:"
if ($LASTEXITCODE -ne 0) {
    throw "無法讀取 gdrive:。請重新執行本腳本並確認 remote 名稱是 gdrive。"
}

Write-Host ""
Write-Host "建立 Google Drive 備份資料夾..."
& $rclone mkdir "gdrive:QuantSaaSBackups"
if ($LASTEXITCODE -ne 0) {
    throw "建立 gdrive:QuantSaaSBackups 失敗。"
}

[Environment]::SetEnvironmentVariable("QUANTSAAS_BACKUP_REMOTE", "gdrive:QuantSaaSBackups", "User")
$env:QUANTSAAS_BACKUP_REMOTE = "gdrive:QuantSaaSBackups"

Write-Host ""
Write-Host "Google Drive 備份目的地已設定完成："
Write-Host "QUANTSAAS_BACKUP_REMOTE = gdrive:QuantSaaSBackups"
Write-Host ""
Write-Host "提醒：如果還沒設定 QUANTSAAS_BACKUP_PASSWORD，排程仍不能自動輸入加密密碼。"

