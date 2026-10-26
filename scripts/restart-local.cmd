@echo off
setlocal
powershell.exe -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File "%~dp0restart-local.ps1" %*
set "RestartExitCode=%errorlevel%"
if not "%RestartExitCode%"=="0" (
    echo.
    echo WzStudy restart failed. Review the error above.
    pause
)
exit /b %RestartExitCode%
