param(
    [ValidateSet("Audit", "Purge")]
    [string]$Mode = "Audit",
    [string]$BackupID = "",
    [long]$ExpectedCount = -1,
    [string]$ExpectedSHA256 = "",
    [switch]$ConfirmPurge
)

. "$PSScriptRoot\..\backup\common.ps1"

$root = Get-ProjectRoot
Set-Location $root
$dsn = Get-ContainerDSN

Write-Host "Current GeneObservation and protected-data audit:"
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @("audit-gene-observations")

if ($Mode -eq "Audit") {
    return
}

if (!$ConfirmPurge) {
    throw "Purge requires a completed full-backup restore drill. Verify the count and hash, then add -ConfirmPurge."
}
if ([string]::IsNullOrWhiteSpace($BackupID) -or $ExpectedCount -lt 0 -or [string]::IsNullOrWhiteSpace($ExpectedSHA256)) {
    throw "Purge requires -BackupID, -ExpectedCount, and -ExpectedSHA256."
}

Write-Host "Backup $BackupID authorizes removal of $ExpectedCount legacy observations. GeneRecord, task summaries, M, candidates, and formal parameters are protected."
Invoke-BackupTool -Root $root -DatabaseDSN $dsn -Arguments @(
    "purge-gene-observations",
    "--backup-id", $BackupID,
    "--expected-count", $ExpectedCount,
    "--expected-sha256", $ExpectedSHA256,
    "--confirm", "DELETE_GENE_OBSERVATIONS"
)
