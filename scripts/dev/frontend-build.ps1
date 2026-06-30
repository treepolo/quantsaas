$ErrorActionPreference = "Stop"
. "$PSScriptRoot\common.ps1"

Set-DevConsole
$root = Get-ProjectRoot
Push-Location (Join-Path $root "web-frontend")
try {
    npm run build
    if ($LASTEXITCODE -ne 0) {
        throw "npm run build failed, exit code: $LASTEXITCODE"
    }
} finally {
    Pop-Location
}
