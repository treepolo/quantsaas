param(
    [switch]$OpenBrowser
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Invoke-ProjectDockerCompose -Root $root -Arguments @("up", "-d", "--build") -DockerTimeoutSeconds 240
if ($OpenBrowser) {
    Open-QuantSaaS
}
Write-Host "QuantSaaS rebuilt and started: http://localhost:8080"
