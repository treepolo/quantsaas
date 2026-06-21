@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "scripts\backup\full_backup.ps1" -Kind weekly-full
pause

