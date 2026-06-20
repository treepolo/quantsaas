param(
  [string]$OutputDir = "backups"
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$backupDir = Join-Path $root $OutputDir
New-Item -ItemType Directory -Force -Path $backupDir | Out-Null

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$outFile = Join-Path $backupDir "quantsaas-local-$stamp.sql"

$env:DOCKER_CONFIG = Join-Path $root ".docker-codex-config"
docker compose --project-name quantsaas exec -T postgres pg_dump -U quantsaas -d quantsaas | Out-File -FilePath $outFile -Encoding utf8

Write-Host "Wrote $outFile"
Write-Host "Upload it to the Oracle VM, then run:"
Write-Host "gzip $outFile"
Write-Host "bash deploy/oracle/restore_db.sh backups/$(Split-Path $outFile -Leaf).gz"
