# Shared Windows foreground-launcher lifecycle helpers. No keys or workspace data.
function Get-LocalRestartSignalName {
    param(
        [int]$LauncherProcessId,
        [DateTime]$LauncherStartTime,
        [int]$Port
    )
    # Start time prevents a recycled PowerShell PID from matching an old launcher.
    return "Local\WzStudy.Restart.$LauncherProcessId.$($LauncherStartTime.ToUniversalTime().Ticks).$Port"
}

function New-LocalRestartSignal {
    param([int]$Port)
    $LauncherProcess = [Diagnostics.Process]::GetCurrentProcess()
    try {
        $Name = Get-LocalRestartSignalName -LauncherProcessId $LauncherProcess.Id -LauncherStartTime $LauncherProcess.StartTime -Port $Port
        return [Threading.EventWaitHandle]::new($false, [Threading.EventResetMode]::ManualReset, $Name)
    } finally {
        $LauncherProcess.Dispose()
    }
}

function Open-LocalRestartSignal {
    param([int]$ServiceProcessId, [int]$Port)
    try {
        $Service = Get-CimInstance Win32_Process -Filter "ProcessId=$ServiceProcessId" -ErrorAction Stop
        if ($null -eq $Service) { return $null }
        $LauncherProcess = Get-Process -Id $Service.ParentProcessId -ErrorAction Stop
        try {
            $Name = Get-LocalRestartSignalName -LauncherProcessId $LauncherProcess.Id -LauncherStartTime $LauncherProcess.StartTime -Port $Port
            return [Threading.EventWaitHandle]::OpenExisting($Name)
        } finally {
            $LauncherProcess.Dispose()
        }
    } catch {
        # Older launchers and manually started executables have no signal. Restart
        # remains possible, but their exit must not be claimed as acknowledged.
        Write-Verbose "No matching local launcher restart signal: $($_.Exception.Message)"
        return $null
    }
}

function Assert-LocalServiceExit {
    param([int]$ExitCode, [string]$ServiceAddress, [bool]$RestartRequested = $false)
    if ($ExitCode -eq 0) { return }
    if ($ExitCode -eq -1 -and $RestartRequested) {
        Write-Host 'This WzStudy instance was stopped by restart-local.ps1. Check the restarting terminal for the replacement service.'
        return
    }
    $UnsignedCode = [BitConverter]::ToUInt32([BitConverter]::GetBytes($ExitCode), 0)
    $Message = 'The service stopped with exit code {0} (0x{1:X8}).' -f $ExitCode, $UnsignedCode
    if ($ExitCode -eq -1) {
        $Message += ' Windows forced termination is possible (for example Stop-Process, an older restart script, or a terminal/task being stopped); the exit code alone does not identify the cause.'
        $Message += " Check http://$ServiceAddress/api/v1/health/ready for a replacement instance before restarting. Keep the foreground terminal open; do not delete the workspace or keys."
    } elseif ($ExitCode -eq -1073741510) {
        $Message += ' Windows reported a console interruption (Ctrl+C or console close). Keep the foreground terminal open while the service is needed.'
    } else {
        $Message += ' Review the service output above for the startup or runtime error. The workspace and original keys should not be reset.'
    }
    throw $Message
}
