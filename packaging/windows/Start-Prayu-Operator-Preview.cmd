@echo off
setlocal
cd /d "%~dp0"
start "Traverse Board" "%~dp0cyberagent-desktop.exe" --operator-preview
endlocal
