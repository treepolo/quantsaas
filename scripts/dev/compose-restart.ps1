param(
    [switch]$OpenBrowser
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Invoke-ProjectDockerCompose -Root $root -Arguments @("restart", "saas")
if ($OpenBrowser) {
    Open-QuantSaaS
}
Write-Host "QuantSaaS SaaS service restarted: http://localhost:8080"
