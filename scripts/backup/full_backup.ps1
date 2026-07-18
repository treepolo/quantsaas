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
$previousDockerConfig = $env:DOCKER_CONFIG
try {
    $env:DOCKER_CONFIG = Join-Path $root ".docker-codex-config"
    docker run --rm `
        --network quantsaas_default `
        -e PGPASSWORD=quantsaas `
        -v "${work}:/backup" `
        postgres:15 `
        pg_dump -h postgres -U quantsaas -d quantsaas --format=plain --no-owner --no-privileges -f /backup/quantsaas-full.sql
    if ($LASTEXITCODE -ne 0) {
        throw "pg_dump client container failed."
    }
} finally {
    if ($null -eq $previousDockerConfig) {
        Remove-Item Env:DOCKER_CONFIG -ErrorAction SilentlyContinue
    } else {
        $env:DOCKER_CONFIG = $previousDockerConfig
    }
}
if (!(Test-Path -LiteralPath $sqlPath) -or (Get-Item -LiteralPath $sqlPath).Length -eq 0) {
    throw "pg_dump did not create a usable SQL file."
}

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
if (Test-Path -LiteralPath $zipPath) {
    Remove-Item -LiteralPath $zipPath -Force
}
# Compress-Archive cannot write archives once an entry stream exceeds 2 GiB.
# The Windows bsdtar client writes a ZIP64 archive that remains compatible with
# Expand-Archive and the existing encrypted .zip.enc restore workflow.
tar.exe -a -cf $zipPath -C $work .
if ($LASTEXITCODE -ne 0 -or !(Test-Path -LiteralPath $zipPath)) {
    throw "ZIP64 archive creation failed."
}

Write-Host "加密全量備份..."
Protect-BackupArchive -Root $root -ZipPath $zipPath -EncryptedPath $encryptedPath -Passphrase $passphrase
Remove-Item -LiteralPath $zipPath -Force

$stateDir = Join-Path $root "backups\state"
New-Item -ItemType Directory -Force -Path $stateDir | Out-Null
$now = (Get-Date).ToUniversalTime().ToString("o")
Set-Content -Path (Join-Path $stateDir "last-full-at.txt") -Encoding UTF8 -Value $now
Set-Content -Path (Join-Path $stateDir "last-incremental-at.txt") -Encoding UTF8 -Value $now

if ($Upload) {
    Publish-BackupToCloud -Root $root -FilePath $encryptedPath -Remote $Remote
}

Write-Host ""
Write-Host "全量備份完成：$encryptedPath"

