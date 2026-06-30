param(
    [switch]$OpenBrowser
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Invoke-ProjectDockerCompose -Root $root -Arguments @("up", "-d")
if ($OpenBrowser) {
    Open-QuantSaaS
}
Write-Host "QuantSaaS is running: http://localhost:8080"
