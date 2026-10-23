param([switch]$Postgres)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if (-not (Test-Path '.env')) { & (Join-Path $PSScriptRoot 'init-env.ps1') }
$allowed = @('ADMIN_API_KEY', 'READ_API_KEY', 'CORS_ORIGINS', 'POLL_ENABLED', 'SSE_MAX_CLIENTS', 'HTTP_ADDR', 'DATABASE_URL')
foreach ($line in Get-Content '.env') {
    if ($line -match '^\s*(#|$)') { continue }
    $parts = $line -split '=', 2
    if ($parts.Length -eq 2 -and $allowed -contains $parts[0].Trim()) {
        $name = $parts[0].Trim()
        if (-not [Environment]::GetEnvironmentVariable($name, 'Process')) {
            [Environment]::SetEnvironmentVariable($name, $parts[1].Trim(), 'Process')
        }
    }
}
$env:APP_ENV = 'development'
if (-not $env:HTTP_ADDR) { $env:HTTP_ADDR = '127.0.0.1:8091' }
if ($Postgres) {
    if (-not $env:DATABASE_URL) { throw 'Set DATABASE_URL in this shell first.' }
    $env:STORE = 'postgres'
    go run ./cmd/migrate
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} else {
    $env:STORE = 'memory'
    Write-Warning 'Memory mode: data is lost when this process stops. Use -Postgres for persistence.'
}
Write-Host "API listen address: $env:HTTP_ADDR. Credentials are in project .env (not printed)."
go run ./cmd/api
exit $LASTEXITCODE
