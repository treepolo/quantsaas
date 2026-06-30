. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
Invoke-ProjectDockerCompose -Root $root -Arguments @("ps")
