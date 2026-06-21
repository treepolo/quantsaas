$ErrorActionPreference = "Stop"

function Get-ProjectRoot {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

function Get-BackupTimestamp {
    return (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
}

function Convert-ToProjectRelative {
    param(
        [string]$Root,
        [string]$Path
    )
    $resolved = (Resolve-Path $Path).Path
    if (!$resolved.StartsWith($Root)) {
        throw "Path is outside project root: $Path"
    }
    return $resolved.Substring($Root.Length + 1).Replace("\", "/")
}

function Read-PlainPassphrase {
    param(
        [string]$Prompt = "Enter backup encryption password"
    )
    $secure = Read-Host $Prompt -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

function Get-BackupPassphrase {
    if (![string]::IsNullOrWhiteSpace($env:QUANTSAAS_BACKUP_PASSWORD)) {
        return $env:QUANTSAAS_BACKUP_PASSWORD
    }
    return Read-PlainPassphrase
}

function Invoke-DockerCompose {
    param(
        [string]$Root,
        [string[]]$Arguments
    )
    $previousDockerConfig = $env:DOCKER_CONFIG
    try {
        $env:DOCKER_CONFIG = Join-Path $Root ".docker-codex-config"
        docker compose --project-name quantsaas @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "docker compose failed, exit code: $LASTEXITCODE"
        }
    } finally {
        if ($null -eq $previousDockerConfig) {
            Remove-Item Env:DOCKER_CONFIG -ErrorAction SilentlyContinue
        } else {
            $env:DOCKER_CONFIG = $previousDockerConfig
        }
    }
}

function Invoke-GoTool {
    param(
        [string]$Root,
        [string]$CommandPath,
        [string[]]$Arguments,
        [string]$DatabaseDSN = "",
        [string]$Passphrase = ""
    )
    $previousDockerConfig = $env:DOCKER_CONFIG
    $previousDSN = $env:DATABASE_DSN
    $previousPassword = $env:EMERGENCY_BUNDLE_PASSWORD
    try {
        $env:DOCKER_CONFIG = Join-Path $Root ".docker-codex-config"
        if ($DatabaseDSN -ne "") {
            $env:DATABASE_DSN = $DatabaseDSN
        }
        if ($Passphrase -ne "") {
            $env:EMERGENCY_BUNDLE_PASSWORD = $Passphrase
        }
        docker run --rm `
            -e DATABASE_DSN `
            -e EMERGENCY_BUNDLE_PASSWORD `
            -v "${Root}:/src" `
            -w /src `
            golang:1.25-alpine `
            go run $CommandPath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$CommandPath failed in Docker, exit code: $LASTEXITCODE"
        }
    } finally {
        if ($null -eq $previousDockerConfig) {
            Remove-Item Env:DOCKER_CONFIG -ErrorAction SilentlyContinue
        } else {
            $env:DOCKER_CONFIG = $previousDockerConfig
        }
        if ($null -eq $previousDSN) {
            Remove-Item Env:DATABASE_DSN -ErrorAction SilentlyContinue
        } else {
            $env:DATABASE_DSN = $previousDSN
        }
        if ($null -eq $previousPassword) {
            Remove-Item Env:EMERGENCY_BUNDLE_PASSWORD -ErrorAction SilentlyContinue
        } else {
            $env:EMERGENCY_BUNDLE_PASSWORD = $previousPassword
        }
    }
}

function Invoke-BackupTool {
    param(
        [string]$Root,
        [string[]]$Arguments,
        [string]$DatabaseDSN
    )
    Invoke-GoTool -Root $Root -CommandPath "./cmd/backup-tool" -Arguments $Arguments -DatabaseDSN $DatabaseDSN
}

function Invoke-EmergencyTool {
    param(
        [string]$Root,
        [string[]]$Arguments,
        [string]$Passphrase
    )
    Invoke-GoTool -Root $Root -CommandPath "./cmd/emergency-signal" -Arguments $Arguments -Passphrase $Passphrase
}

function Get-ContainerDSN {
    return "host=host.docker.internal user=quantsaas password=quantsaas dbname=quantsaas port=5432 sslmode=disable TimeZone=Asia/Taipei"
}

function New-BackupWorkspace {
    param(
        [string]$Root,
        [string]$Kind,
        [string]$Timestamp
    )
    $work = Join-Path $Root "backups\work\$Kind-$Timestamp"
    New-Item -ItemType Directory -Force -Path $work | Out-Null
    return $work
}

function Protect-BackupArchive {
    param(
        [string]$Root,
        [string]$ZipPath,
        [string]$EncryptedPath,
        [string]$Passphrase
    )
    New-Item -ItemType Directory -Force -Path (Split-Path $EncryptedPath -Parent) | Out-Null
    $zipRel = Convert-ToProjectRelative -Root $Root -Path $ZipPath
    $encParent = Split-Path $EncryptedPath -Parent
    if (!(Test-Path $encParent)) {
        New-Item -ItemType Directory -Force -Path $encParent | Out-Null
    }
    $encRel = $EncryptedPath.Substring($Root.Length + 1).Replace("\", "/")
    Invoke-EmergencyTool -Root $Root -Passphrase $Passphrase -Arguments @(
        "encrypt",
        "--in", $zipRel,
        "--out", $encRel
    )
}

function Unprotect-BackupArchive {
    param(
        [string]$Root,
        [string]$EncryptedPath,
        [string]$ZipPath,
        [string]$Passphrase
    )
    New-Item -ItemType Directory -Force -Path (Split-Path $ZipPath -Parent) | Out-Null
    $encRel = Convert-ToProjectRelative -Root $Root -Path $EncryptedPath
    $zipRel = $ZipPath.Substring($Root.Length + 1).Replace("\", "/")
    Invoke-EmergencyTool -Root $Root -Passphrase $Passphrase -Arguments @(
        "decrypt",
        "--in", $encRel,
        "--out", $zipRel
    )
}

function Publish-BackupToCloud {
    param(
        [string]$Root,
        [string]$FilePath,
        [string]$Remote
    )
    if ([string]::IsNullOrWhiteSpace($Remote)) {
        Write-Host "未設定 QUANTSAAS_BACKUP_REMOTE，已只保留本機加密備份。"
        return
    }
    $rclone = Get-RclonePath -Root $Root
    if ([string]::IsNullOrWhiteSpace($rclone)) {
        throw "已設定雲端目的地，但找不到 rclone。請先安裝 rclone，或清空 QUANTSAAS_BACKUP_REMOTE。"
    }
    & $rclone copy $FilePath $Remote
    if ($LASTEXITCODE -ne 0) {
        throw "rclone upload failed, exit code: $LASTEXITCODE"
    }
    Write-Host "已上傳到雲端：$Remote"
}

function Get-RclonePath {
    param(
        [string]$Root
    )
    $local = Join-Path $Root ".tools\rclone\rclone.exe"
    if (Test-Path $local) {
        return $local
    }
    $command = Get-Command rclone -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }
    return ""
}

function Write-JsonFile {
    param(
        [string]$Path,
        [hashtable]$Data
    )
    $Data | ConvertTo-Json -Depth 8 | Set-Content -Path $Path -Encoding UTF8
}

