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
# All ordinary local starts use the same PostgreSQL-backed implementation.
& (Join-Path $PSScriptRoot 'start-postgres-local.ps1') @PSBoundParameters
