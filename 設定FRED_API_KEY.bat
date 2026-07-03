@echo off
chcp 65001 >nul
powershell -NoProfile -ExecutionPolicy Bypass -Command "$key = Read-Host 'Paste FRED API Key'; if ([string]::IsNullOrWhiteSpace($key)) { Write-Host 'No API key entered. Cancelled.'; exit 1 }; $key = $key.Trim(); [Environment]::SetEnvironmentVariable('FRED_API_KEY', $key, 'User'); $envPath = Join-Path (Get-Location) '.env'; $lines = @(); if (Test-Path $envPath) { $lines = Get-Content $envPath | Where-Object { $_ -notmatch '^FRED_API_KEY=' } }; $lines += ('FRED_API_KEY=' + $key); Set-Content -LiteralPath $envPath -Value $lines -Encoding ascii; Write-Host 'FRED_API_KEY saved to Windows user environment variables and project .env. Please restart QuantSaaS.'"
pause
