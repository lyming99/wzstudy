[CmdletBinding()]
param(
    [string]$Version = '0.4.0'
)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root 'dist'
$AgentExe = Join-Path $Root 'internal\agentbundle\assets\wzstudy-agent.exe'
$ServerExe = Join-Path $Dist 'wzstudy.exe'
$MigrateExe = Join-Path $Dist 'wzstudy-migrate.exe'
$VersionedServer = Join-Path $Dist "wzstudy-full-v$Version-windows-amd64.exe"
$Stage = Join-Path $Dist "wzstudy-full-v$Version-windows-amd64"
$Archive = Join-Path $Dist "wzstudy-full-v$Version-windows-amd64.zip"

Write-Host '1/5 Build collection agent...'
Push-Location (Join-Path $Root 'agent')
try {
    & go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $AgentExe ./cmd/wzstudy-agent
    if ($LASTEXITCODE -ne 0) { throw 'agent build failed' }
} finally { Pop-Location }

Write-Host '2/5 Build server with the embedded agent...'
Push-Location $Root
try {
    & go build -trimpath -o $ServerExe ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw 'server build failed' }
    & go build -trimpath -o $MigrateExe ./cmd/migrate
    if ($LASTEXITCODE -ne 0) { throw 'migration build failed' }
    Copy-Item -LiteralPath $ServerExe -Destination $VersionedServer -Force
} finally { Pop-Location }

Write-Host '3/5 Stage release files...'
if (Test-Path -LiteralPath $Stage) { Remove-Item -LiteralPath $Stage -Recurse -Force }
New-Item -ItemType Directory -Path $Stage -Force | Out-Null
foreach ($name in @('start-local.ps1', 'start-local.cmd', 'start-postgres-local.ps1', 'start-postgres-local.cmd', '.env.example', 'README.md', 'LICENSE')) {
    $source = Join-Path $Root $name
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $Stage }
}
$Scripts = Join-Path $Stage 'scripts'
New-Item -ItemType Directory -Path $Scripts -Force | Out-Null
foreach ($name in @(
    'admin-environment.ps1',
    'local-env.ps1',
    'local-postgres-state.ps1',
    'local-service-lifecycle.ps1',
    'start-local.ps1',
    'restart-local.ps1',
    'restart-local.cmd',
    'docker-compose-local-postgres.yml'
)) {
    $source = Join-Path $Root "scripts\$name"
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $Scripts }
}
$StageDist = Join-Path $Stage 'dist'
New-Item -ItemType Directory -Path $StageDist -Force | Out-Null
Copy-Item -LiteralPath $ServerExe -Destination (Join-Path $StageDist 'wzstudy.exe')
Copy-Item -LiteralPath $MigrateExe -Destination (Join-Path $StageDist 'wzstudy-migrate.exe')
$Docs = Join-Path $Stage 'docs'
New-Item -ItemType Directory -Path $Docs -Force | Out-Null
foreach ($name in @('AGENT_PROTOCOL.md', 'AGENT_COLLECTION.md', 'VERIFICATION_AGENT.md', 'PROJECT_LAYOUT.md', 'API.md', 'RUNNING.md')) {
    $source = Join-Path $Root "docs\$name"
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $Docs }
}
$AgentDir = Join-Path $Stage 'agent'
New-Item -ItemType Directory -Path $AgentDir -Force | Out-Null
Copy-Item -LiteralPath $AgentExe -Destination (Join-Path $AgentDir 'wzstudy-agent.exe')
foreach ($name in @('agent.env.example', 'start-agent.ps1', 'start-agent.cmd', 'AGENT_README.md')) {
    $source = Join-Path $Dist $name
    if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination $AgentDir }
}
$Skills = Join-Path $AgentDir 'skills'
New-Item -ItemType Directory -Path $Skills -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $Dist 'skills\web-article') -Destination $Skills -Recurse -Force
New-Item -ItemType Directory -Path (Join-Path $AgentDir 'work') -Force | Out-Null

Write-Host '4/5 Create archive...'
if (Test-Path -LiteralPath $Archive) { Remove-Item -LiteralPath $Archive -Force }
Compress-Archive -Path (Join-Path $Stage '*') -DestinationPath $Archive -CompressionLevel Optimal

Write-Host '5/5 Done.'
$hash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
$size = [math]::Round((Get-Item -LiteralPath $Archive).Length / 1MB, 1)
Write-Host "Archive: $Archive"
Write-Host "Size: $size MB"
Write-Host "SHA256: $hash"
