. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Set-Location $root
Ensure-PlainBundle -Root $root

Invoke-GoTool -Root $root -Arguments @(
    "latest",
    "--bundle", "emergency/soxl-21.bundle.json"
)

Write-Host ""
Write-Host "Result also written to emergency\soxl-21-latest.md"
