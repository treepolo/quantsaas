param(
    [switch]$Push
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root

New-Item -ItemType Directory -Force -Path (Join-Path $root "emergency") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $root "secure-backups\emergency") | Out-Null

$passphrase = Read-ConfirmedPassphrase
$dsn = "host=host.docker.internal user=quantsaas password=quantsaas dbname=quantsaas port=5432 sslmode=disable TimeZone=Asia/Taipei"

Write-Host ""
Write-Host "Exporting SOXL #21 emergency bundle..."
Invoke-GoTool -Root $root -Arguments @(
    "export",
    "--parameter-id", "21",
    "--dsn", $dsn,
    "--out", "emergency/soxl-21.bundle.json"
)

Write-Host ""
Write-Host "Creating encrypted backup..."
Invoke-GoTool -Root $root -Passphrase $passphrase -Arguments @(
    "encrypt",
    "--in", "emergency/soxl-21.bundle.json",
    "--out", "secure-backups/emergency/soxl-21.bundle.json.enc"
)

Ensure-ManualPrices -Root $root -Passphrase $passphrase
Encrypt-ManualPrices -Root $root -Passphrase $passphrase

Write-Host ""
Write-Host "Generating latest signal..."
Invoke-GoTool -Root $root -Arguments @(
    "latest",
    "--bundle", "emergency/soxl-21.bundle.json"
)

if ($Push) {
    Write-Host ""
    Write-Host "Committing and pushing encrypted backup to GitHub..."
    Push-EncryptedEmergencyBackups -Root $root -Message "Update encrypted SOXL emergency backups"
}

Write-Host ""
Write-Host "Done."
Write-Host "Plain bundle: emergency\soxl-21.bundle.json (not committed)"
Write-Host "Encrypted backup: secure-backups\emergency\soxl-21.bundle.json.enc"
Write-Host "Encrypted manual prices: secure-backups\emergency\soxl-21-manual-prices.jsonl.enc"
