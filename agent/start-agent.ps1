param(
    [ValidateSet('doctor', 'run', 'once', 'version')]
    [string]$Command = 'doctor'
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$config = Join-Path $root 'agent.env'
if (-not (Test-Path -LiteralPath $config)) {
    $template = Join-Path $root 'agent.env.example'
    if (Test-Path -LiteralPath $template) {
        Copy-Item -LiteralPath $template -Destination $config
        Write-Host "已生成配置文件：$config"
        Write-Host '请填写 WZSTUDY_AGENT_KEY 与模型配置后重新运行。'
        exit 1
    }
    throw "缺少 agent.env，请先从 agent.env.example 复制一份。"
}
$exe = Join-Path $root 'wzstudy-agent.exe'
if (-not (Test-Path -LiteralPath $exe)) {
    throw "缺少 wzstudy-agent.exe，请将压缩包完整解压后再运行。"
}
$env:AGENT_CONFIG = $config
& $exe $Command
exit $LASTEXITCODE
