$ErrorActionPreference = "Stop"

function Get-ProjectRoot {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

function Read-PlainPassphrase {
    param(
        [string]$Prompt = "Enter emergency backup password"
    )
    $secure = Read-Host $Prompt -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

function Read-ConfirmedPassphrase {
    $first = Read-PlainPassphrase "Set emergency backup password"
    $second = Read-PlainPassphrase "Enter password again"
    if ($first -ne $second) {
        throw "Passwords do not match. Cancelled."
    }
    if ([string]::IsNullOrWhiteSpace($first)) {
        throw "Password cannot be empty."
    }
    return $first
}

function Invoke-GoTool {
    param(
        [string]$Root,
        [string[]]$Arguments,
        [string]$Passphrase = ""
    )
    $previousDockerConfig = $env:DOCKER_CONFIG
    $previousPassword = $env:EMERGENCY_BUNDLE_PASSWORD
    try {
        $env:DOCKER_CONFIG = Join-Path $Root ".docker-codex-config"
        if ($Passphrase -ne "") {
            $env:EMERGENCY_BUNDLE_PASSWORD = $Passphrase
        }
        docker run --rm `
            -e EMERGENCY_BUNDLE_PASSWORD `
            -v "${Root}:/src" `
            -w /src `
            golang:1.25-alpine `
            go run ./cmd/emergency-signal @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "emergency-signal failed in Docker, exit code: $LASTEXITCODE"
        }
    } finally {
        if ($null -eq $previousDockerConfig) {
            Remove-Item Env:DOCKER_CONFIG -ErrorAction SilentlyContinue
        } else {
            $env:DOCKER_CONFIG = $previousDockerConfig
        }
        if ($null -eq $previousPassword) {
            Remove-Item Env:EMERGENCY_BUNDLE_PASSWORD -ErrorAction SilentlyContinue
        } else {
            $env:EMERGENCY_BUNDLE_PASSWORD = $previousPassword
        }
    }
}

function Ensure-PlainBundle {
    param(
        [string]$Root,
        [string]$Passphrase = ""
    )
    $bundle = Join-Path $Root "emergency\soxl-21.bundle.json"
    $encrypted = Join-Path $Root "secure-backups\emergency\soxl-21.bundle.json.enc"
    if (Test-Path $bundle) {
        return
    }
    if (!(Test-Path $encrypted)) {
        throw "Plain bundle and encrypted backup are both missing. Run the export backup batch file first."
    }
    if ($Passphrase -eq "") {
        $Passphrase = Read-PlainPassphrase "Plain bundle missing. Enter password to decrypt backup"
    }
    Invoke-GoTool -Root $Root -Passphrase $Passphrase -Arguments @(
        "decrypt",
        "--in", "secure-backups/emergency/soxl-21.bundle.json.enc",
        "--out", "emergency/soxl-21.bundle.json"
    )
}

function Ensure-ManualPrices {
    param(
        [string]$Root,
        [string]$Passphrase = ""
    )
    $manual = Join-Path $Root "emergency\soxl-21-manual-prices.jsonl"
    $encrypted = Join-Path $Root "secure-backups\emergency\soxl-21-manual-prices.jsonl.enc"
    if (Test-Path $manual) {
        return
    }
    if (!(Test-Path $encrypted)) {
        return
    }
    if ($Passphrase -eq "") {
        $Passphrase = Read-PlainPassphrase "Manual prices backup found. Enter password to restore it"
    }
    Invoke-GoTool -Root $Root -Passphrase $Passphrase -Arguments @(
        "decrypt",
        "--in", "secure-backups/emergency/soxl-21-manual-prices.jsonl.enc",
        "--out", "emergency/soxl-21-manual-prices.jsonl"
    )
}

function Encrypt-ManualPrices {
    param(
        [string]$Root,
        [string]$Passphrase
    )
    $manual = Join-Path $Root "emergency\soxl-21-manual-prices.jsonl"
    if (!(Test-Path $manual)) {
        return
    }
    if ([string]::IsNullOrWhiteSpace($Passphrase)) {
        throw "Password is required to encrypt manual prices."
    }
    Invoke-GoTool -Root $Root -Passphrase $Passphrase -Arguments @(
        "encrypt",
        "--in", "emergency/soxl-21-manual-prices.jsonl",
        "--out", "secure-backups/emergency/soxl-21-manual-prices.jsonl.enc"
    )
}

function Push-EncryptedEmergencyBackups {
    param(
        [string]$Root,
        [string]$Message = "Update encrypted SOXL emergency backups"
    )
    Set-Location $Root
    $paths = @()
    if (Test-Path (Join-Path $Root "secure-backups\emergency\soxl-21.bundle.json.enc")) {
        $paths += "secure-backups/emergency/soxl-21.bundle.json.enc"
    }
    if (Test-Path (Join-Path $Root "secure-backups\emergency\soxl-21-manual-prices.jsonl.enc")) {
        $paths += "secure-backups/emergency/soxl-21-manual-prices.jsonl.enc"
    }
    if ($paths.Count -eq 0) {
        Write-Host "No encrypted emergency backup files found."
        return
    }
    git add @paths
    git diff --cached --quiet
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Encrypted emergency backups did not change; no commit needed."
        return
    }
    git commit -m $Message
    if ($LASTEXITCODE -ne 0) {
        throw "git commit failed."
    }
    git push
    if ($LASTEXITCODE -ne 0) {
        throw "git push failed."
    }
}
