$ErrorActionPreference = "Stop"
. "$PSScriptRoot\common.ps1"

Set-DevConsole
$root = Get-ProjectRoot
Invoke-ProjectDockerCompose -Root $root -Arguments @("build") -DockerTimeoutSeconds 240
Write-Host "QuantSaaS images rebuilt without starting services."
