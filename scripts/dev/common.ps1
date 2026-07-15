$ErrorActionPreference = "Stop"

function Set-DevConsole {
    try {
        chcp 65001 | Out-Null
    } catch {
    }
    try {
        [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new()
        $script:OutputEncoding = [System.Text.UTF8Encoding]::new()
    } catch {
    }
}

function Get-ProjectRoot {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

function Set-ProjectDockerConfig {
    param(
        [string]$Root
    )
    $dockerConfig = Join-Path $Root ".docker-codex-config"
    if (!(Test-Path $dockerConfig)) {
        New-Item -ItemType Directory -Force -Path $dockerConfig | Out-Null
    }
    $env:DOCKER_CONFIG = $dockerConfig
}

function Start-DockerDesktopIfNeeded {
    if ($script:DockerDesktopStartAttempted) {
        return
    }
    $script:DockerDesktopStartAttempted = $true
    $running = Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -like "Docker Desktop*" -or $_.ProcessName -like "com.docker.backend*" } | Select-Object -First 1
    if ($running) {
        Write-Host "Docker Desktop is starting or already running. Waiting for engine..."
        return
    }
    $dockerDesktopCandidates = @(
        (Join-Path $env:ProgramFiles "Docker\Docker\Docker Desktop.exe"),
        "C:\Program Files\Docker\Docker\Docker Desktop.exe"
    )
    foreach ($candidate in $dockerDesktopCandidates) {
        if ($candidate -and (Test-Path $candidate)) {
            Write-Host "Starting Docker Desktop..."
            Start-Process -FilePath $candidate -WindowStyle Minimized | Out-Null
            return
        }
    }
}

function Wait-DockerEngine {
    param(
        [int]$TimeoutSeconds = 180
    )
    $script:DockerDesktopStartAttempted = $false
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $previousErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        try {
            # `docker info` may emit capability warnings even while the engine is
            # healthy. Querying the server version is a smaller, stable readiness
            # probe and still fails when only the client is available.
            docker version --format "{{.Server.Version}}" *> $null
            $exitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        if ($exitCode -eq 0) {
            return
        }
        Start-DockerDesktopIfNeeded
        Start-Sleep -Seconds 3
    }
    throw "Docker engine is not ready. Please confirm Docker Desktop is installed and can start."
}

function Invoke-ProjectDockerCompose {
    param(
        [string]$Root,
        [string[]]$Arguments,
        [int]$DockerTimeoutSeconds = 180
    )
    Set-DevConsole
    Set-ProjectDockerConfig -Root $Root
    Wait-DockerEngine -TimeoutSeconds $DockerTimeoutSeconds
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        docker compose --project-name quantsaas @Arguments
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if ($exitCode -ne 0) {
        throw "docker compose failed, exit code: $exitCode"
    }
}

function Invoke-ProjectGoDocker {
    param(
        [string]$Root,
        [string[]]$GoArguments,
        [int]$DockerTimeoutSeconds = 180
    )
    Set-DevConsole
    Set-ProjectDockerConfig -Root $Root
    Wait-DockerEngine -TimeoutSeconds $DockerTimeoutSeconds
    $goModCache = Join-Path $Root ".cache\gomod"
    $goBuildCache = Join-Path $Root ".cache\gobuild"
    New-Item -ItemType Directory -Force -Path $goModCache, $goBuildCache | Out-Null
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $environmentArgs = @()
    if (![string]::IsNullOrWhiteSpace($env:TEST_DATABASE_DSN)) {
        # Let Docker inherit the value without copying the DSN into the command
        # line, where process inspection could expose it.
        $environmentArgs = @("-e", "TEST_DATABASE_DSN")
    }
    try {
        docker run --rm `
            @environmentArgs `
            -v "${Root}:/src" `
            -v "${goModCache}:/go/pkg/mod" `
            -v "${goBuildCache}:/root/.cache/go-build" `
            -w /src `
            golang:1.25-alpine `
            go @GoArguments
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if ($exitCode -ne 0) {
        throw "go command failed in Docker, exit code: $exitCode"
    }
}

function Open-QuantSaaS {
    Start-Process "http://localhost:8080" | Out-Null
}
