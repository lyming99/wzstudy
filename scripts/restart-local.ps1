[CmdletBinding()]
param(
    [ValidateRange(1, 65535)][int]$DatabasePort = 15432,
    [ValidateRange(1, 65535)][int]$ApiPort = 8091,
    [string]$AdministratorEmail = 'admin@wzstudy.local',
    [ValidateRange(10, 600)][int]$StartupTimeoutSeconds = 120,
    [string]$Address = '',
    [switch]$Lan,
    [string]$DataDirectory = '',
    [string]$ProfileDirectory = '',
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check,
    [ValidateRange(1, 120)][int]$StopTimeoutSeconds = 15
)

# Stop only executables belonging to this project, then start PostgreSQL mode.
$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'local-service-lifecycle.ps1')
$Launcher = Join-Path $Root 'start-local.ps1'
$LaunchParameters = @{}
foreach ($Name in @('DatabasePort','ApiPort','AdministratorEmail','StartupTimeoutSeconds','Address','Lan','DataDirectory','ProfileDirectory','InitializeAdmin','NoEnvFile','SkipBuild')) {
    if ($PSBoundParameters.ContainsKey($Name)) { $LaunchParameters[$Name] = $PSBoundParameters[$Name] }
}
$DiagnosticParameters = $LaunchParameters.Clone()
$DiagnosticParameters['Check'] = $true
$Configuration = & $Launcher @DiagnosticParameters | ConvertFrom-Json
if ($Check) {
    $Configuration | ConvertTo-Json -Compress
    return
}
$Port = [int]$Configuration.api_port
if (-not (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)) {
    throw 'This Windows restart script requires Get-NetTCPConnection. No process was stopped.'
}
# Check prerequisites before interrupting the existing service. The actual
# launcher still performs the full local-context, profile and volume checks.
$docker = Get-Command docker -ErrorAction SilentlyContinue
if (-not $docker) { throw 'Docker is required for PostgreSQL. No process was stopped.' }
$hasSource = (Test-Path -LiteralPath (Join-Path $Root 'go.mod') -PathType Leaf) -and (Get-Command go -ErrorAction SilentlyContinue) -and -not $SkipBuild
if (-not $hasSource -and (-not (Test-Path -LiteralPath (Join-Path $Root 'dist\wzstudy-migrate.exe') -PathType Leaf) -or -not (Test-Path -LiteralPath (Join-Path $Root 'dist\wzstudy-v0.3.0-windows-amd64.exe') -PathType Leaf))) {
    throw 'Go source or both packaged PostgreSQL executables are required. No process was stopped.'
}
if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'docker-compose-local-postgres.yml') -PathType Leaf)) {
    throw 'The PostgreSQL Compose file is missing. No process was stopped.'
}
function Invoke-DockerPreflight([string[]]$Arguments) {
    $old = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output = @(& $docker.Source @Arguments 2>&1)
        return [PSCustomObject]@{ code = $LASTEXITCODE; text = (($output | ForEach-Object { $_.ToString() }) -join "`n") }
    } finally { $ErrorActionPreference = $old }
}
$endpoint = [Environment]::GetEnvironmentVariable('DOCKER_HOST', 'Process')
$context = [Environment]::GetEnvironmentVariable('DOCKER_CONTEXT', 'Process')
if ($endpoint -and $context) { throw 'Ambiguous Docker target. No process was stopped.' }
if (-not $endpoint) {
    $current = Invoke-DockerPreflight @('context', 'show')
    if ($current.code -ne 0 -or -not $current.text.Trim()) { throw 'Could not determine the Docker context. No process was stopped.' }
    $inspection = Invoke-DockerPreflight @('context', 'inspect', $current.text.Trim())
    if ($inspection.code -ne 0) { throw 'Could not inspect the Docker context. No process was stopped.' }
    try { $contexts = @($inspection.text | ConvertFrom-Json) } catch { throw 'Invalid Docker context metadata. No process was stopped.' }
    if ($contexts.Count -ne 1) { throw 'Unexpected Docker context metadata. No process was stopped.' }
    $endpoint = [string]$contexts[0].Endpoints.docker.Host
}
if ($endpoint -notmatch '(?i)^npipe:////\./pipe/[a-z0-9_.-]+$' -and $endpoint -notmatch '^unix:///[^\s]+$') {
    throw 'A local Docker context is required. No process was stopped.'
}
if ((Invoke-DockerPreflight @('version', '--format', '{{.Server.Version}}')).code -ne 0 -or
    (Invoke-DockerPreflight @('compose', 'version', '--short')).code -ne 0) {
    throw 'Docker engine and Compose v2 must be running. No process was stopped.'
}

$LegacyExecutables = @(
    [IO.Path]::GetFullPath((Join-Path $Root 'bin\wzstudy-dev.exe')),
    [IO.Path]::GetFullPath((Join-Path $Root 'dist\wzstudy-v0.3.0-windows-amd64.exe'))
)
$BuildPattern = '^' + [regex]::Escape([IO.Path]::GetFullPath($Configuration.profile_directory).TrimEnd('\') + '\') + '\.build-[a-f0-9]{32}\\wzstudy-api\.exe$'
function Get-ListenerProcessIds {
    Get-NetTCPConnection -State Listen -ErrorAction Stop |
        Where-Object { $_.LocalPort -eq $Port } |
        Select-Object -ExpandProperty OwningProcess -Unique
}
$ServiceProcesses = @()
foreach ($ProcessId in @(Get-ListenerProcessIds)) {
    $ServiceProcess = Get-Process -Id $ProcessId -ErrorAction Stop
    $ExecutablePath = $ServiceProcess.Path
    if ([string]::IsNullOrWhiteSpace($ExecutablePath)) {
        throw "Port $Port has an unrecognized owner. No process was stopped."
    }
    $ExecutablePath = [IO.Path]::GetFullPath($ExecutablePath)
    if ($LegacyExecutables -notcontains $ExecutablePath -and $ExecutablePath -notmatch $BuildPattern) {
        throw "Port $Port is owned by process $ProcessId, which is not a recognized executable in this project. No process was stopped."
    }
    $ServiceProcesses += $ServiceProcess
}
if ($ServiceProcesses.Count -gt 0) {
    Write-Warning 'Restart interrupts active requests. Do not restart during an upload or ingest. File-mode data is not migrated to PostgreSQL.'
    foreach ($ServiceProcess in $ServiceProcesses) {
        Write-Host "Stopping WzStudy (PID $($ServiceProcess.Id))..."
        $RestartSignal = Open-LocalRestartSignal -ServiceProcessId $ServiceProcess.Id -Port $Port
        try {
            if ($null -ne $RestartSignal) { $null = $RestartSignal.Set() }
            Stop-Process -InputObject $ServiceProcess -Force -ErrorAction Stop
            if (-not $ServiceProcess.WaitForExit($StopTimeoutSeconds * 1000)) { throw "Timed out waiting for process $($ServiceProcess.Id) to exit. The service was not started." }
        } catch {
            if ($null -ne $RestartSignal) { $null = $RestartSignal.Reset() }
            throw
        } finally {
            if ($null -ne $RestartSignal) { $RestartSignal.Dispose() }
        }
    }
    $Deadline = [DateTime]::UtcNow.AddSeconds($StopTimeoutSeconds)
    while (@(Get-ListenerProcessIds).Count -gt 0) {
        if ([DateTime]::UtcNow -ge $Deadline) { throw "Port $Port was not released. The service was not started." }
        Start-Sleep -Milliseconds 200
    }
    # The previous foreground launcher releases its profile lock in finally.
    $LockPath = Join-Path $Configuration.profile_directory 'startup.lock'
    while (Test-Path -LiteralPath $LockPath -PathType Leaf) {
        $lock = $null
        try {
            $lock = [IO.File]::Open($LockPath, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
            break
        } catch {
            if ([DateTime]::UtcNow -ge $Deadline) { throw 'The prior PostgreSQL launcher did not release its profile lock. The service was not started.' }
            Start-Sleep -Milliseconds 200
        } finally { if ($lock) { $lock.Dispose() } }
    }
} else {
    Write-Host "No service is listening on port $Port; starting WzStudy."
}
Write-Host 'Starting WzStudy with PostgreSQL...'
& $Launcher @LaunchParameters
