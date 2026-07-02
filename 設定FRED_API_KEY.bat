@echo off
chcp 65001 >nul
powershell -NoProfile -ExecutionPolicy Bypass -Command "$key = Read-Host 'Paste FRED API Key'; if ([string]::IsNullOrWhiteSpace($key)) { Write-Host 'No API key entered. Cancelled.'; exit 1 }; [Environment]::SetEnvironmentVariable('FRED_API_KEY', $key.Trim(), 'User'); Write-Host 'FRED_API_KEY saved to Windows user environment variables. Please restart QuantSaaS.'"
pause
