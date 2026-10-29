# Compatibility entry point; PostgreSQL is mandatory for local service starts.
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
    [switch]$Check
)
$ErrorActionPreference = 'Stop'
if ($PSBoundParameters.ContainsKey('DataDirectory') -and -not [string]::IsNullOrWhiteSpace($DataDirectory) -and -not [IO.Path]::IsPathRooted($DataDirectory)) {
    $PSBoundParameters['DataDirectory'] = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot $DataDirectory))
}
if (-not $Check) { Write-Warning 'Compatibility launcher: prefer start-local.ps1 in the project root.' }
& (Join-Path (Split-Path $PSScriptRoot -Parent) 'start-local.ps1') @PSBoundParameters
