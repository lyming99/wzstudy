[CmdletBinding()]
param(
    [string]$Address = '127.0.0.1:8091',
    [string]$DataDirectory = '',
    [string]$ProfileDirectory = '',
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check
)
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts\start-local.ps1') @PSBoundParameters
