param(
    [string]$Since = "",
    [string]$Remote = $env:QUANTSAAS_BACKUP_REMOTE,
    [switch]$Upload
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root

$timestamp = Get-BackupTimestamp
$work = New-BackupWorkspace -Root $root -Kind "daily-incremental" -Timestamp $timestamp
$targetDir = Join-Path $root "backups\incremental"
New-Item -ItemType Directory -Force -Path $targetDir | Out-Null

$stateDir = Join-Path $root "backups\state"
$statePath = Join-Path $stateDir "last-incremental-at.txt"
if ([string]::IsNullOrWhiteSpace($Since)) {
    if (Test-Path $statePath) {
        $Since = (Get-Content -Path $statePath -Encoding UTF8 | Select-Object -First 1).Trim()
    } else {
        $Since = (Get-Date).ToUniversalTime().AddDays(-1).ToString("o")
    }
}

$jsonPath = Join-Path $work "incremental.json"
$zipPath = Join-Path $targetDir "daily-incremental-$timestamp.zip"
$encryptedPath = "$zipPath.enc"
$passphrase = Get-BackupPassphrase
$dsn = Get-ContainerDSN

Write-Host "啟動 Postgres..."
Invoke-DockerCompose -Root $root -Arguments @("up", "-d", "postgres")

Write-Host "建立增量備份，起點：$Since"
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @(
    "export-incremental",
    "--since", $Since,
    "--out", "backups/work/daily-incremental-$timestamp/incremental.json"
)

$manifest = @{
    version = 1
    kind = "daily-incremental"
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    since = $Since
    contents = @(
        "research_instruments",
        "k_lines",
        "dataset_metadata",
        "daily_execution_snapshots",
        "gene_records",
        "gene_observations",
        "evolution_tasks",
        "backtest_runs"
    )
    excluded = @(
        "Docker images",
        "node_modules",
        "temporary files",
        "exchange API credentials"
    )
}
Write-JsonFile -Path (Join-Path $work "manifest.json") -Data $manifest

Write-Host "壓縮增量備份..."
Compress-Archive -Path (Join-Path $work "*") -DestinationPath $zipPath -Force

Write-Host "加密增量備份..."
Protect-BackupArchive -Root $root -ZipPath $zipPath -EncryptedPath $encryptedPath -Passphrase $passphrase
Remove-Item -LiteralPath $zipPath -Force

New-Item -ItemType Directory -Force -Path $stateDir | Out-Null
Set-Content -Path $statePath -Encoding UTF8 -Value (Get-Date).ToUniversalTime().ToString("o")

if ($Upload) {
    Publish-BackupToCloud -Root $root -FilePath $encryptedPath -Remote $Remote
}

Write-Host ""
Write-Host "增量備份完成：$encryptedPath"


