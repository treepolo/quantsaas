@echo off
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\emergency\export_soxl21_backup.ps1" -Push
pause

