param(
    [Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)]
    [string[]]$Path
)

. "$PSScriptRoot\common.ps1"

$root = Get-ProjectRoot
$paths = @()
foreach ($item in $Path) {
    foreach ($part in ($item -split ",")) {
        $part = $part.Trim()
        if ($part -ne "") {
            $paths += $part
        }
    }
}
$args = @("fmt") + $paths
Invoke-ProjectGoDocker -Root $root -GoArguments $args
