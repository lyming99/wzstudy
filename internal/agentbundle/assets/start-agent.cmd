@echo off
setlocal
cd /d "%~dp0"
if "%~1"=="" (set "COMMAND=doctor") else (set "COMMAND=%~1")
if not exist "agent.env" (
    if exist "agent.env.example" (
        copy /y "agent.env.example" "agent.env" >nul
        echo 已生成 agent.env，请填写 WZSTUDY_AGENT_KEY 与模型配置后重新运行。
        pause
        exit /b 1
    )
    echo 缺少 agent.env，请从 agent.env.example 复制一份。
    pause
    exit /b 1
)
if not exist "wzstudy-agent.exe" (
    echo 缺少 wzstudy-agent.exe，请将压缩包完整解压后再运行。
    pause
    exit /b 1
)
set "AGENT_CONFIG=%CD%\agent.env"
wzstudy-agent.exe %COMMAND%
set "CODE=%ERRORLEVEL%"
if "%COMMAND%"=="doctor" pause
exit /b %CODE%
