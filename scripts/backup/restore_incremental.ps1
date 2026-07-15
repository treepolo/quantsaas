param(
    [Parameter(Mandatory = $true)]
    [string]$Archive
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root

$timestamp = Get-BackupTimestamp
$work = New-BackupWorkspace -Root $root -Kind "restore-incremental" -Timestamp $timestamp
$archivePath = (Resolve-Path $Archive).Path

if ($archivePath.EndsWith(".enc")) {
    $zipPath = Join-Path $work "restore-incremental.zip"
    $passphrase = Get-BackupPassphrase
    Write-Host "解密增量備份..."
    Unprotect-BackupArchive -Root $root -EncryptedPath $archivePath -ZipPath $zipPath -Passphrase $passphrase
} else {
    $zipPath = $archivePath
}

Write-Host "解壓增量備份..."
Expand-Archive -Path $zipPath -DestinationPath $work -Force

$json = Get-ChildItem -Path $work -Recurse -Filter "incremental.json" | Select-Object -First 1
if ($null -eq $json) {
    throw "備份包裡找不到 incremental.json。"
}

Invoke-DockerCompose -Root $root -Arguments @("up", "-d", "postgres")

$relative = Convert-ToProjectRelative -Root $root -Path $json.FullName
$dsn = Get-ContainerDSN
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @(
    "import-incremental",
    "--in", $relative
)
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @("verify-backtests")
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @("verify-compute-tasks")

Write-Host ""
Write-Host "增量還原完成，標準化回測、報酬分析與計算任務內容 hash／引用已驗證：$Archive"

