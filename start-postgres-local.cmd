@echo off
setlocal
powershell.exe -NoLogo -NoProfile -ExecutionPolicy RemoteSigned -File "%~dp0start-postgres-local.ps1" %*
set "EXITCODE=%ERRORLEVEL%"
if not "%EXITCODE%"=="0" (
    echo.
    echo Startup failed. Review the message above. Existing database data was not deleted.
    pause
)
endlocal & exit /b %EXITCODE%
