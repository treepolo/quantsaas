param(
    [string]$Kind = "weekly-full",
    [string]$Remote = $env:QUANTSAAS_BACKUP_REMOTE,
    [switch]$Upload
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root

$timestamp = Get-BackupTimestamp
$work = New-BackupWorkspace -Root $root -Kind $Kind -Timestamp $timestamp
$targetDir = Join-Path $root "backups\full"
New-Item -ItemType Directory -Force -Path $targetDir | Out-Null

$sqlPath = Join-Path $work "quantsaas-full.sql"
$zipPath = Join-Path $targetDir "$Kind-$timestamp.zip"
$encryptedPath = "$zipPath.enc"
$passphrase = Get-BackupPassphrase

Write-Host "啟動 Postgres..."
Invoke-DockerCompose -Root $root -Arguments @("up", "-d", "postgres")

Write-Host "建立 PostgreSQL 全量備份..."
Invoke-DockerCompose -Root $root -Arguments @(
    "exec", "-T", "postgres",
    "sh", "-c",
    "pg_dump -U quantsaas -d quantsaas --format=plain --no-owner --no-privileges > /tmp/quantsaas-full.sql"
)

$containerId = (& docker compose --project-name quantsaas ps -q postgres).Trim()
if ([string]::IsNullOrWhiteSpace($containerId)) {
    throw "找不到 postgres container。"
}
$containerSource = "$($containerId):/tmp/quantsaas-full.sql"
docker cp $containerSource $sqlPath
if ($LASTEXITCODE -ne 0) {
    throw "docker cp failed."
}
Invoke-DockerCompose -Root $root -Arguments @("exec", "-T", "postgres", "rm", "-f", "/tmp/quantsaas-full.sql")

$manifest = @{
    version = 1
    kind = $Kind
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    database = "quantsaas"
    contents = @(
        "PostgreSQL plain SQL dump",
        "config.yaml template",
        "docker-compose.yml",
        "restore notes"
    )
    excluded = @(
        "Docker images",
        "node_modules",
        "temporary files",
        "exchange API credentials"
    )
}
Write-JsonFile -Path (Join-Path $work "manifest.json") -Data $manifest

Copy-Item -Path (Join-Path $root "docker-compose.yml") -Destination (Join-Path $work "docker-compose.yml") -Force
Copy-Item -Path (Join-Path $root "config.yaml") -Destination (Join-Path $work "config.yaml") -Force
Set-Content -Path (Join-Path $work "README.txt") -Encoding UTF8 -Value @"
QuantSaaS full backup

Restore with:
  scripts\backup\restore_full.ps1 -Archive "$encryptedPath" -ConfirmRestore

This archive intentionally excludes exchange API credentials.
"@

Write-Host "壓縮全量備份..."
Compress-Archive -Path (Join-Path $work "*") -DestinationPath $zipPath -Force

Write-Host "加密全量備份..."
Protect-BackupArchive -Root $root -ZipPath $zipPath -EncryptedPath $encryptedPath -Passphrase $passphrase
Remove-Item -LiteralPath $zipPath -Force

$stateDir = Join-Path $root "backups\state"
New-Item -ItemType Directory -Force -Path $stateDir | Out-Null
$now = (Get-Date).ToUniversalTime().ToString("o")
Set-Content -Path (Join-Path $stateDir "last-full-at.txt") -Encoding UTF8 -Value $now
Set-Content -Path (Join-Path $stateDir "last-incremental-at.txt") -Encoding UTF8 -Value $now

if ($Upload) {
    Publish-BackupToCloud -FilePath $encryptedPath -Remote $Remote
}

Write-Host ""
Write-Host "全量備份完成：$encryptedPath"

