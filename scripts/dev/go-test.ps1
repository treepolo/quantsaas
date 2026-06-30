param(
    [string[]]$Target = @("./internal/..."),
    [string[]]$ExtraArgs = @("-count=1")
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
$targets = @()
foreach ($item in $Target) {
    foreach ($part in ($item -split ",")) {
        $part = $part.Trim()
        if ($part -ne "") {
            $targets += $part
        }
    }
}
$args = @("test") + $targets + $ExtraArgs
Invoke-ProjectGoDocker -Root $root -GoArguments $args
