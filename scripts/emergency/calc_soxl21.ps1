. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root
Ensure-PlainBundle -Root $root

$date = Read-Host "Enter SOXL close date, for example 2026-06-22"
if ([string]::IsNullOrWhiteSpace($date)) {
    throw "Date cannot be empty."
}
$close = Read-Host "Enter SOXL close price"
if ([string]::IsNullOrWhiteSpace($close)) {
    throw "Close price cannot be empty."
}

Invoke-GoTool -Root $root -Arguments @(
    "calc",
    "--bundle", "emergency/soxl-21.bundle.json",
    "--date", $date,
    "--close", $close
)

Write-Host ""
Write-Host "Result also written to emergency\soxl-21-latest.md"
