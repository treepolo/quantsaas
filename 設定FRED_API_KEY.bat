@echo off
chcp 65001 >nul
powershell -NoProfile -ExecutionPolicy Bypass -Command "$key = Read-Host '請貼上 FRED API Key'; if ([string]::IsNullOrWhiteSpace($key)) { Write-Host '沒有輸入 API Key，已取消。'; exit 1 }; [Environment]::SetEnvironmentVariable('FRED_API_KEY', $key.Trim(), 'User'); Write-Host 'FRED_API_KEY 已設定到 Windows 使用者環境變數。請重新啟動軟體或重新建置並啟動。'"
pause
