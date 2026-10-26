[CmdletBinding()]
param(
    [string]$Address = '0.0.0.0:8091',
    [string]$DataDirectory = '',
    [string]$ProfileDirectory = '',
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check,
    [ValidateRange(1, 120)]
    [int]$StopTimeoutSeconds = 15
)

# Restart only the local file-mode service, using the canonical launcher's paths.
$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'local-service-lifecycle.ps1')
$Launcher = Join-Path $PSScriptRoot 'start-local.ps1'
$LaunchParameters = @{}
foreach ($Name in @('Address', 'DataDirectory', 'ProfileDirectory', 'InitializeAdmin', 'NoEnvFile', 'SkipBuild')) {
    if ($PSBoundParameters.ContainsKey($Name)) { $LaunchParameters[$Name] = $PSBoundParameters[$Name] }
}

# Resolve profiles before stopping anything. -Check never reads keys or stops a process.
$DiagnosticParameters = $LaunchParameters.Clone()
$DiagnosticParameters['Check'] = $true
$Configuration = & $Launcher @DiagnosticParameters | ConvertFrom-Json
if ($Check) {
    $Configuration | ConvertTo-Json -Compress
    return
}

if ($Address -notmatch ':(\d+)$') { throw 'Use -Address in host:port form, for example 127.0.0.1:8091.' }
$Port = [int]$Matches[1]
if ($Port -lt 1 -or $Port -gt 65535) { throw 'The port must be between 1 and 65535.' }
if (-not (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)) {
    throw 'This Windows restart script requires Get-NetTCPConnection. No process was stopped.'
}

# Exact paths only: never kill a process merely because its name or port matches.
$DevelopmentExecutable = [IO.Path]::GetFullPath((Join-Path $Root 'bin\wzstudy-dev.exe'))
$PackagedExecutable = [IO.Path]::GetFullPath((Join-Path $Root 'dist\wzstudy-v0.3.0-windows-amd64.exe'))
$AllowedExecutables = @($DevelopmentExecutable, $PackagedExecutable)
if ($Configuration.executable -ne $DevelopmentExecutable -and
    -not (Test-Path -LiteralPath $Configuration.executable -PathType Leaf)) {
    throw 'The packaged executable is missing. Install Go or provide the packaged executable before restarting.'
}

function Get-ListenerProcessIds {
    # Query listeners first so an empty port is not confused with a failed query.
    Get-NetTCPConnection -State Listen -ErrorAction Stop |
        Where-Object { $_.LocalPort -eq $Port } |
        Select-Object -ExpandProperty OwningProcess -Unique
}

$ServiceProcesses = @()
foreach ($ProcessId in @(Get-ListenerProcessIds)) {
    $ServiceProcess = Get-Process -Id $ProcessId -ErrorAction Stop
    $ExecutablePath = $ServiceProcess.Path
    if ([string]::IsNullOrWhiteSpace($ExecutablePath) -or
        $AllowedExecutables -notcontains [IO.Path]::GetFullPath($ExecutablePath)) {
        throw "Port $Port is owned by process $ProcessId, which is not a recognized executable in this project. No process was stopped."
    }
    $ServiceProcesses += $ServiceProcess
}

# Validate every listener before stopping any of them (including IPv4/IPv6 owners).
if ($ServiceProcesses.Count -gt 0) {
    Write-Warning 'Restart interrupts active requests. Do not restart during an upload or ingest.'
    foreach ($ServiceProcess in $ServiceProcesses) {
        Write-Host "Stopping WzStudy (PID $($ServiceProcess.Id))..."
        # Acknowledge this explicit restart in the old foreground launcher, so
        # its expected Windows -1 exit is not confused with an unexplained kill.
        $RestartSignal = Open-LocalRestartSignal -ServiceProcessId $ServiceProcess.Id -Port $Port
        try {
            if ($null -ne $RestartSignal) { $null = $RestartSignal.Set() }
            Stop-Process -InputObject $ServiceProcess -Force -ErrorAction Stop
            if (-not $ServiceProcess.WaitForExit($StopTimeoutSeconds * 1000)) {
                throw "Timed out waiting for process $($ServiceProcess.Id) to exit. The service was not started."
            }
        } catch {
            if ($null -ne $RestartSignal) { $null = $RestartSignal.Reset() }
            throw
        } finally {
            if ($null -ne $RestartSignal) { $RestartSignal.Dispose() }
        }
    }
    $Deadline = [DateTime]::UtcNow.AddSeconds($StopTimeoutSeconds)
    while (@(Get-ListenerProcessIds).Count -gt 0) {
        if ([DateTime]::UtcNow -ge $Deadline) {
            throw "Port $Port was not released. The service was not started."
        }
        Start-Sleep -Milliseconds 200
    }
} else {
    Write-Host "No service is listening on port $Port; starting WzStudy."
}

Write-Host 'Starting WzStudy with the existing local launcher...'
& $Launcher @LaunchParameters
