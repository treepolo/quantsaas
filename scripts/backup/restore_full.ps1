param(
    [Parameter(Mandatory = $true)]
    [string]$Archive,
    [switch]$ConfirmRestore
)

. "$PSScriptRoot\common.ps1"

if (!$ConfirmRestore) {
    throw "全量還原會清空目前 PostgreSQL public schema。若確定要執行，請加上 -ConfirmRestore。"
}

$root = Get-ProjectRoot
Set-Location $root

$timestamp = Get-BackupTimestamp
$work = New-BackupWorkspace -Root $root -Kind "restore-full" -Timestamp $timestamp
$archivePath = (Resolve-Path $Archive).Path

if ($archivePath.EndsWith(".enc")) {
    $zipPath = Join-Path $work "restore-full.zip"
    $passphrase = Get-BackupPassphrase
    Write-Host "解密全量備份..."
    Unprotect-BackupArchive -Root $root -EncryptedPath $archivePath -ZipPath $zipPath -Passphrase $passphrase
} else {
    $zipPath = $archivePath
}

Write-Host "解壓全量備份..."
Expand-Archive -Path $zipPath -DestinationPath $work -Force

$sql = Get-ChildItem -Path $work -Recurse -Filter "quantsaas-full.sql" | Select-Object -First 1
if ($null -eq $sql) {
    throw "備份包裡找不到 quantsaas-full.sql。"
}

Write-Host "啟動 Postgres..."
Invoke-DockerCompose -Root $root -Arguments @("up", "-d", "postgres")

$containerId = (& docker compose --project-name quantsaas ps -q postgres).Trim()
if ([string]::IsNullOrWhiteSpace($containerId)) {
    throw "找不到 postgres container。"
}

Write-Host "複製 SQL 到 Postgres container..."
$containerTarget = "$($containerId):/tmp/quantsaas-restore.sql"
docker cp $sql.FullName $containerTarget
if ($LASTEXITCODE -ne 0) {
    throw "docker cp failed."
}

Write-Host "清空目標 schema..."
Invoke-DockerCompose -Root $root -Arguments @(
    "exec", "-T", "postgres",
    "psql", "-U", "quantsaas", "-d", "quantsaas",
    "-v", "ON_ERROR_STOP=1",
    "-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
)

Write-Host "匯入全量備份..."
Invoke-DockerCompose -Root $root -Arguments @(
    "exec", "-T", "postgres",
    "psql", "-U", "quantsaas", "-d", "quantsaas",
    "-v", "ON_ERROR_STOP=1",
    "-f", "/tmp/quantsaas-restore.sql"
)
Invoke-DockerCompose -Root $root -Arguments @("exec", "-T", "postgres", "rm", "-f", "/tmp/quantsaas-restore.sql")

Write-Host "驗證標準化回測結果與報酬分析報告的引用及內容 hash..."
$dsn = Get-ContainerDSN
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @("verify-backtests")
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @("verify-compute-tasks")

Write-Host ""
Write-Host "全量還原完成。接著可以依時間順序套用全量之後的每日增量備份。"

