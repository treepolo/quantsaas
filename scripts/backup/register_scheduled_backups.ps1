. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
$dailyScript = Join-Path $root "scripts\backup\incremental_backup.ps1"
$weeklyScript = Join-Path $root "scripts\backup\full_backup.ps1"

$dailyTask = "QuantSaaS Daily Incremental Backup"
$weeklyTask = "QuantSaaS Weekly Full Backup"

$dailyAction = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$dailyScript`" -Upload"
$weeklyAction = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$weeklyScript`" -Kind weekly-full -Upload"

schtasks /Create /TN $dailyTask /SC DAILY /ST 08:10 /TR $dailyAction /F
if ($LASTEXITCODE -ne 0) {
    throw "建立每日增量備份排程失敗。"
}

schtasks /Create /TN $weeklyTask /SC WEEKLY /D SUN /ST 09:10 /TR $weeklyAction /F
if ($LASTEXITCODE -ne 0) {
    throw "建立每週全量備份排程失敗。"
}

Write-Host ""
Write-Host "已建立排程："
Write-Host "- $dailyTask：每天 08:10"
Write-Host "- $weeklyTask：每週日 09:10"
Write-Host ""
if ([string]::IsNullOrWhiteSpace($env:QUANTSAAS_BACKUP_PASSWORD)) {
    Write-Host "提醒：排程執行時無法互動輸入密碼。"
    Write-Host "請在 Windows 使用者環境變數設定 QUANTSAAS_BACKUP_PASSWORD，否則排程可能卡住或失敗。"
}
if ([string]::IsNullOrWhiteSpace($env:QUANTSAAS_BACKUP_REMOTE)) {
    Write-Host "提醒：尚未設定 QUANTSAAS_BACKUP_REMOTE，因此排程會只產出本機加密備份。"
}

