@echo off
setlocal
cd /d "%~dp0"
set /p ARCHIVE=請貼上增量備份 .zip.enc 路徑：
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "scripts\backup\restore_incremental.ps1" -Archive "%ARCHIVE%"
pause

