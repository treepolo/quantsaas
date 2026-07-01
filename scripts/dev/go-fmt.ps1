param(
    [Parameter(Mandatory = $true)]
    [string[]]$Path
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
$args = @("fmt") + $Path
Invoke-ProjectGoDocker -Root $root -GoArguments $args
