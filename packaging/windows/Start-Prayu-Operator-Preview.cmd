@echo off
setlocal
cd /d "%~dp0"
start "Traverse Board" "%~dp0TraverseBoard.exe" --operator-preview
endlocal
