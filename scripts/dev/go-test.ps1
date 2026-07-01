param(
    [string]$Target = "./internal/...",
    [string[]]$ExtraArgs = @("-count=1")
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
$args = @("test", $Target) + $ExtraArgs
Invoke-ProjectGoDocker -Root $root -GoArguments $args
