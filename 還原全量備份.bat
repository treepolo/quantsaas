@echo off
setlocal
cd /d "%~dp0"
set /p ARCHIVE=請貼上全量備份 .zip.enc 路徑：
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "scripts\backup\restore_full.ps1" -Archive "%ARCHIVE%" -ConfirmRestore
pause

