@echo off
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\dev\compose-rebuild.ps1" -OpenBrowser
pause
