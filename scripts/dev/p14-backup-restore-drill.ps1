param(
    [string]$Kind = "p14-retirement"
)

. "$PSScriptRoot\..\backup\common.ps1"

$root = Get-ProjectRoot
Set-Location $root
$timestamp = Get-BackupTimestamp
$database = "quantsaas_p14_restore_$($timestamp.Replace('-', '_'))"
$work = New-BackupWorkspace -Root $root -Kind "p14-restore-drill" -Timestamp $timestamp
$archive = $null

function Read-Audit([string]$DatabaseDSN) {
    $raw = Invoke-BackupTool -Root $root -DatabaseDSN $DatabaseDSN -Arguments @("audit-gene-observations")
    return (($raw -join "`n") | ConvertFrom-Json)
}

try {
    Write-Host "Reading source observation and protected-data audit..."
    $sourceAudit = Read-Audit -DatabaseDSN (Get-ContainerDSN)

    Write-Host "Creating encrypted full backup..."
    & "$root\scripts\backup\full_backup.ps1" -Kind $Kind
    $archive = Get-ChildItem -LiteralPath (Join-Path $root "backups\full") -Filter "$Kind-*.zip.enc" | Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1
    if ($null -eq $archive) { throw "The new P14 full backup was not found." }

    $zipPath = Join-Path $work "restore.zip"
    $passphrase = Get-BackupPassphrase
    Unprotect-BackupArchive -Root $root -EncryptedPath $archive.FullName -ZipPath $zipPath -Passphrase $passphrase
    Expand-Archive -LiteralPath $zipPath -DestinationPath $work -Force
    $sql = Get-ChildItem -LiteralPath $work -Recurse -Filter "quantsaas-full.sql" | Select-Object -First 1
    if ($null -eq $sql) { throw "quantsaas-full.sql is missing from the backup." }

    Invoke-DockerCompose -Root $root -Arguments @("up", "-d", "postgres")
    docker run --rm --network quantsaas_default -e PGPASSWORD=quantsaas postgres:15 createdb -h postgres -U quantsaas $database
    if ($LASTEXITCODE -ne 0) { throw "Creating the isolated restore database failed." }
    docker run --rm --network quantsaas_default -e PGPASSWORD=quantsaas -v "${work}:/backup" postgres:15 psql -h postgres -U quantsaas -d $database -v ON_ERROR_STOP=1 -f /backup/quantsaas-full.sql
    if ($LASTEXITCODE -ne 0) { throw "Restoring the isolated database failed." }

    $restoredDSN = (Get-ContainerDSN).Replace("dbname=quantsaas", "dbname=$database")
    $restoredAudit = Read-Audit -DatabaseDSN $restoredDSN
    if ($sourceAudit.count -ne $restoredAudit.count -or $sourceAudit.export_sha256 -ne $restoredAudit.export_sha256 -or $sourceAudit.earliest_created_at -ne $restoredAudit.earliest_created_at -or $sourceAudit.latest_created_at -ne $restoredAudit.latest_created_at) {
        throw "Restored observation count, hash, or time range does not match."
    }
    if (($sourceAudit.protected_counts | ConvertTo-Json -Compress) -ne ($restoredAudit.protected_counts | ConvertTo-Json -Compress) -or ($sourceAudit.gene_role_counts | ConvertTo-Json -Compress) -ne ($restoredAudit.gene_role_counts | ConvertTo-Json -Compress)) {
        throw "Restored protected-data counts do not match."
    }

    $backupID = [IO.Path]::GetFileNameWithoutExtension([IO.Path]::GetFileNameWithoutExtension($archive.Name))
    $receipt = [ordered]@{
        version = 1
        backup_id = $backupID
        archive = $archive.Name
        archive_sha256 = (Get-FileHash -LiteralPath $archive.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        restored_at = (Get-Date).ToUniversalTime().ToString("o")
        gene_observations = $restoredAudit
        restore_database = $database
        result = "passed"
    }
    $stateDir = Join-Path $root "backups\state"
    New-Item -ItemType Directory -Force -Path $stateDir | Out-Null
    $receiptPath = Join-Path $stateDir "$backupID-restore-drill.json"
    $receipt | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $receiptPath -Encoding UTF8
    Write-Host "P14 full backup and isolated restore drill passed: $backupID"
    Write-Host "Receipt: $receiptPath"
} finally {
    try { docker run --rm --network quantsaas_default -e PGPASSWORD=quantsaas postgres:15 dropdb -h postgres -U quantsaas --if-exists $database } catch {}
    if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}
