# Compatibility entry point for v0.2.0 workspaces. New entry: ../start-local.ps1.
[CmdletBinding()]
param(
    [string]$Address = '0.0.0.0:8091',
    [string]$DataDirectory = '',
    [string]$ProfileDirectory = '',
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check
)
$ErrorActionPreference = 'Stop'
# Preserve the old meaning of relative -DataDirectory arguments.
if ($PSBoundParameters.ContainsKey('DataDirectory') -and -not [string]::IsNullOrWhiteSpace($DataDirectory) -and -not [IO.Path]::IsPathRooted($DataDirectory)) {
    $PSBoundParameters['DataDirectory'] = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot $DataDirectory))
}
if (-not $Check) { Write-Warning 'Compatibility launcher: prefer start-local.ps1 in the project root.' }
& (Join-Path (Split-Path $PSScriptRoot -Parent) 'scripts\start-local.ps1') @PSBoundParameters
