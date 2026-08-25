@echo off
setlocal
cd /d "%~dp0"
rem Compatibility filename: the product EXE now starts the safe control plane directly.
start "Traverse Board" "%~dp0TraverseBoard.exe"
endlocal
