[CmdletBinding()]
param(
    [switch]$Postgres,
    [switch]$Memory,
    [string]$Address = '0.0.0.0:8091',
    [string]$DataDirectory = '',
    [string]$ProfileDirectory = '',
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check
)
$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
if ($Postgres -and $Memory) { throw 'Choose either -Postgres or -Memory, not both.' }
# Default is the same durable local workspace as the root entry point.
if (-not $Postgres -and -not $Memory) {
    $null = $PSBoundParameters.Remove('Postgres')
    $null = $PSBoundParameters.Remove('Memory')
    & (Join-Path $PSScriptRoot 'start-local.ps1') @PSBoundParameters
    return
}
$Mode = if ($Postgres) { 'postgres' } else { 'memory' }
if ($Check) {
    [PSCustomObject]@{ project_root=$Root; store=$Mode; env_file=(Join-Path $Root '.env'); address=$Address; explicit_mode=$true } | ConvertTo-Json -Compress
    return
}
if ($InitializeAdmin) { throw 'Interactive -InitializeAdmin is for local file mode. For -Postgres or -Memory configure SYSTEM_ADMIN_EMAIL and SYSTEM_ADMIN_PASSWORD in environment or project .env.' }
if (-not [string]::IsNullOrWhiteSpace($ProfileDirectory)) { throw '-ProfileDirectory is for file mode; PostgreSQL/memory use project .env or process environment.' }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go 1.25+ is required for explicit development modes.' }
$Allowed = @('ADMIN_API_KEY','READ_API_KEY','CORS_ORIGINS','POLL_ENABLED','HTTP_ADDR','DATABASE_URL','DATA_DIR','UPLOAD_MAX_MB','SYSTEM_ADMIN_EMAIL','SYSTEM_ADMIN_PASSWORD','SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME','BOOTSTRAP_ADMIN_EMAIL','BOOTSTRAP_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')
$Previous = @{}
foreach ($Name in ($Allowed + @('APP_ENV','STORE'))) { $Previous[$Name] = [Environment]::GetEnvironmentVariable($Name,'Process') }
Push-Location $Root
try {
    . (Join-Path $PSScriptRoot 'admin-environment.ps1')
    $ResolvedAdmin = Read-AdministratorEnvironment -ProjectRoot $Root -NoEnvFile:$NoEnvFile
    foreach ($Name in $ResolvedAdmin.Keys) { [Environment]::SetEnvironmentVariable($Name,$ResolvedAdmin[$Name],'Process') }
    if (-not $NoEnvFile) {
    if (-not (Test-Path -LiteralPath '.env')) { & (Join-Path $PSScriptRoot 'init-env.ps1') }
    foreach ($Line in Get-Content -LiteralPath '.env') {
        if ($Line -match '^\s*(#|$)') { continue }
        $Parts = $Line -split '=',2
        if ($Parts.Length -eq 2 -and $Allowed -contains $Parts[0].Trim() -and $Parts[0].Trim() -notmatch '^(SYSTEM_ADMIN_|BOOTSTRAP_ADMIN_)') {
            $Name = $Parts[0].Trim()
            if (-not [Environment]::GetEnvironmentVariable($Name,'Process')) { $Value=$Parts[1];if($Value.Length -ge 2 -and (($Value.StartsWith('"') -and $Value.EndsWith('"')) -or ($Value.StartsWith("'") -and $Value.EndsWith("'")))){$Value=$Value.Substring(1,$Value.Length-2)};if($Name -notmatch 'PASSWORD$'){$Value=$Value.Trim()};[Environment]::SetEnvironmentVariable($Name,$Value,'Process') }
        }
    }
    }
    $env:APP_ENV = 'development'
    $env:STORE = $Mode
    if ($PSBoundParameters.ContainsKey('Address') -or -not $env:HTTP_ADDR) { $env:HTTP_ADDR=$Address }
    if (-not [string]::IsNullOrWhiteSpace($DataDirectory)) {
        $env:DATA_DIR = if ([IO.Path]::IsPathRooted($DataDirectory)) { $DataDirectory } else { Join-Path $Root $DataDirectory }
    } elseif (-not $env:DATA_DIR) {
        $env:DATA_DIR = if ($Postgres) { Join-Path $Root '.local\postgres-data' } else { Join-Path $Root '.tmp\memory-data' }
    }
    if ($Postgres) {
        if (-not $env:DATABASE_URL) { throw 'Configure DATABASE_URL before using -Postgres.' }
        & go run ./cmd/migrate
        if ($LASTEXITCODE -ne 0) { throw 'Database migration failed.' }
    } else { Write-Warning 'Explicit memory mode: metadata is lost when this process stops. Use the default file mode for persistent local work.' }
    Write-Host "Development mode: $Mode; address: $env:HTTP_ADDR. Keys are not printed."
    & go run ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw 'Development service stopped with an error.' }
} finally {
    foreach ($Name in $Previous.Keys) { [Environment]::SetEnvironmentVariable($Name,$Previous[$Name],'Process') }
    if ($ResolvedAdmin) { foreach ($Name in @($ResolvedAdmin.Keys)) { $ResolvedAdmin[$Name] = $null } }
    Pop-Location
}
