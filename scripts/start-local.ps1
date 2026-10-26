[CmdletBinding()]
param(
    [string]$Address = "0.0.0.0:8091",
    [string]$DataDirectory = "",
    [string]$ProfileDirectory = "",
    [switch]$InitializeAdmin,
    [switch]$NoEnvFile,
    [switch]$SkipBuild,
    [switch]$Check
)

# Canonical Windows local launcher. Root wrapper and legacy wrapper delegate here.
# File storage is single-process development storage, never Internet production.
$ErrorActionPreference = "Stop"
$Root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'local-service-lifecycle.ps1')
function Resolve-ProjectPath([string]$Path) {
    if ([IO.Path]::IsPathRooted($Path)) { return [IO.Path]::GetFullPath($Path) }
    return [IO.Path]::GetFullPath((Join-Path $Root $Path))
}
function Test-Profile([string]$Path) {
    return ((Test-Path -LiteralPath (Join-Path $Path 'access-keys.json')) -or
            (Test-Path -LiteralPath (Join-Path $Path 'data\workspace.json')) -or
            (Test-Path -LiteralPath (Join-Path $Path 'data\uploads')))
}
$RootProfile = Join-Path $Root '.local'
$LegacyProfile = Join-Path $Root 'internal\.local'
$UsingLegacy = $false
if (-not [string]::IsNullOrWhiteSpace($ProfileDirectory)) {
    $LocalDirectory = Resolve-ProjectPath $ProfileDirectory
} else {
    $HasRoot = Test-Profile $RootProfile
    $HasLegacy = Test-Profile $LegacyProfile
    if ($HasRoot -and $HasLegacy) {
        throw 'Both .local and internal/.local contain a workspace. Specify -ProfileDirectory explicitly; neither will be overwritten.'
    }
    if ($HasLegacy) { $LocalDirectory = $LegacyProfile; $UsingLegacy = $true }
    else { $LocalDirectory = $RootProfile }
}
if ([string]::IsNullOrWhiteSpace($DataDirectory)) {
    $DataDirectory = Join-Path $LocalDirectory 'data'
} else { $DataDirectory = Resolve-ProjectPath $DataDirectory }
$CredentialFile = Join-Path $LocalDirectory 'access-keys.json'
$Distribution = Join-Path $Root 'dist'
$Executable = Join-Path $Distribution 'wzstudy-v0.3.0-windows-amd64.exe'
$BuildFromSource = (-not $SkipBuild -and (Test-Path -LiteralPath (Join-Path $Root 'go.mod')) -and (Get-Command go -ErrorAction SilentlyContinue))
if ($BuildFromSource) { $Executable = Join-Path $Root 'bin\wzstudy-dev.exe' }

# Read-only diagnostics: does not read keys, build, create files, or start a server.
if ($Check) {
    [PSCustomObject]@{
        project_root = $Root
        profile_directory = $LocalDirectory
        data_directory = $DataDirectory
        credential_file = $CredentialFile
        credentials_exist = (Test-Path -LiteralPath $CredentialFile)
        executable = $Executable
        legacy_profile = $UsingLegacy
        address = $Address
    } | ConvertTo-Json -Compress
    return
}
if ($Address -match ':(\d+)$' -and (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)) {
    $Port = [int]$Matches[1]
    if (@(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue).Count -gt 0) {
        throw "Port $Port is already in use. Keep the running service, or explicitly choose a different -Address and an isolated -ProfileDirectory."
    }
}
if ($UsingLegacy) {
    Write-Warning 'Using the existing internal/.local workspace for compatibility. It has not been reset or moved.'
}
if (-not (Test-Path -LiteralPath $CredentialFile)) {
    if ((Test-Path -LiteralPath (Join-Path $DataDirectory 'workspace.json')) -or
        (Test-Path -LiteralPath (Join-Path $DataDirectory 'uploads'))) {
        throw 'Existing data was found without its credential file. Restore the original keys or choose the correct -ProfileDirectory; new keys will not be generated over existing data.'
    }
}
New-Item -ItemType Directory -Path $LocalDirectory, $Distribution -Force | Out-Null
function New-LocalSecret {
    $Bytes = New-Object byte[] 36
    $Generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $Generator.GetBytes($Bytes) } finally { $Generator.Dispose() }
    return [Convert]::ToBase64String($Bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}
if (-not (Test-Path -LiteralPath $CredentialFile)) {
    $Credentials = @{
        admin_api_key = New-LocalSecret
        read_api_key = New-LocalSecret
        created_at = [DateTime]::UtcNow.ToString('o')
    }
    $Credentials | ConvertTo-Json | Set-Content -LiteralPath $CredentialFile -Encoding UTF8
    if ($env:OS -eq 'Windows_NT') {
        try {
            $Identity = [Security.Principal.WindowsIdentity]::GetCurrent()
            $Acl = Get-Acl -LiteralPath $CredentialFile
            $Acl.SetAccessRuleProtection($true, $false)
            $Rule = New-Object Security.AccessControl.FileSystemAccessRule($Identity.User, 'FullControl', 'Allow')
            $Acl.SetOwner($Identity.User)
            $Acl.SetAccessRule($Rule)
            Set-Acl -LiteralPath $CredentialFile -AclObject $Acl
        } catch {
            Remove-Item -LiteralPath $CredentialFile -Force
            throw 'Could not restrict the local credential file. Configure keys securely before starting.'
        }
    }
} else { $Credentials = Get-Content -LiteralPath $CredentialFile -Raw | ConvertFrom-Json }
if ($Credentials.admin_api_key.Length -lt 32 -or $Credentials.admin_api_key.Length -gt 256 -or
    $Credentials.read_api_key.Length -lt 32 -or $Credentials.read_api_key.Length -gt 256 -or
    $Credentials.admin_api_key -eq $Credentials.read_api_key) {
    throw 'Invalid local credential file. It will not be replaced automatically.'
}
$Variables = @{
    APP_ENV = 'development'
    STORE = 'file'
    DATA_DIR = $DataDirectory
    HTTP_ADDR = $Address
    ADMIN_API_KEY = $Credentials.admin_api_key
    READ_API_KEY = $Credentials.read_api_key
    POLL_ENABLED = 'true'
    CORS_ORIGINS = ''
    UPLOAD_MAX_MB = '200'
}
# Only administrator bootstrap variables are read from an optional project .env.
# Existing process environment wins. Password whitespace is deliberately preserved.
$AdminNames = @('SYSTEM_ADMIN_EMAIL','SYSTEM_ADMIN_PASSWORD','SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME','BOOTSTRAP_ADMIN_EMAIL','BOOTSTRAP_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')
 . (Join-Path $PSScriptRoot 'admin-environment.ps1')
$ResolvedAdmin = Read-AdministratorEnvironment -ProjectRoot $Root -NoEnvFile:$NoEnvFile
$AdminEnvironment = @{}
foreach ($Name in $AdminNames) { $AdminEnvironment[$Name] = $null }
foreach ($Name in $ResolvedAdmin.Keys) { $AdminEnvironment[$Name] = $ResolvedAdmin[$Name] }
if ($InitializeAdmin) {
    Write-Host 'First administrator initialization only; an existing password will never be reset.'
    $Email = Read-Host 'Administrator email'
    $Secret = Read-Host 'Administrator password (8-128 UTF-8 bytes; recommend 12+ characters)' -AsSecureString
    $Confirmation = Read-Host 'Confirm administrator password' -AsSecureString
    $First = [IntPtr]::Zero; $Second = [IntPtr]::Zero
    try {
        $First = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secret)
        $Second = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Confirmation)
        $Plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($First)
        $Again = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($Second)
        if ($Plain -cne $Again) { throw 'Passwords do not match.' }
        $AdminEnvironment['SYSTEM_ADMIN_EMAIL'] = $Email.Trim()
        $AdminEnvironment['SYSTEM_ADMIN_PASSWORD'] = $Plain
        $Plain = $null; $Again = $null
    } finally {
        if ($First -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($First) }
        if ($Second -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($Second) }
        $Secret.Dispose(); $Confirmation.Dispose()
    }
}
foreach ($Name in $AdminNames) { $Variables[$Name] = $AdminEnvironment[$Name] }
$Previous = @{}
foreach ($Name in $Variables.Keys) { $Previous[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process') }
$RestartSignal = $null
Push-Location $Root
try {
    if ($BuildFromSource) {
        New-Item -ItemType Directory -Path (Split-Path $Executable -Parent) -Force | Out-Null
        Write-Host 'Building WzStudy v0.3.0 (development binary in bin/)...'
        & go build -trimpath -o $Executable ./cmd/api
        if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    } elseif (-not (Test-Path -LiteralPath $Executable)) {
        throw 'Go 1.25+ or the packaged Windows executable is required.'
    }
    foreach ($Name in $Variables.Keys) { [Environment]::SetEnvironmentVariable($Name, $Variables[$Name], 'Process') }
    $BrowserAddress = $Address -replace '^0\.0\.0\.0:', '127.0.0.1:' -replace '^\[::\]:', '[::1]:' -replace '^:', '127.0.0.1:'
    Write-Host ''
    Write-Host "Listening: $Address"
    Write-Host "Management (this computer): http://$BrowserAddress/admin/" -ForegroundColor Green
    if ($Address -match '^(0\.0\.0\.0:|\[::\]:|:)') {
        Write-Host 'LAN access: use this computer''s LAN IP and the same port, not 0.0.0.0. Allow the port only on trusted networks if required by your firewall.' -ForegroundColor Yellow
    }
    Write-Host 'Browser sign-in: administrator email and password. API keys are not login passwords.' -ForegroundColor Green
    Write-Host "Keys: $CredentialFile"
    Write-Host "Data: $DataDirectory"
    Write-Host 'Press Ctrl+C to stop. Back up the data directory and original keys together.'
    Write-Host 'Keep this foreground terminal open. A restart from another terminal stops this instance.'
    Write-Host ''
    if ($env:OS -eq 'Windows_NT' -and $Address -match ':(\d+)$') {
        $RestartSignal = New-LocalRestartSignal -Port ([int]$Matches[1])
    }
    & $Executable
    $ServiceExitCode = $LASTEXITCODE
    $RestartWasRequested = ($null -ne $RestartSignal -and $RestartSignal.WaitOne(0))
    Assert-LocalServiceExit -ExitCode $ServiceExitCode -ServiceAddress $BrowserAddress -RestartRequested $RestartWasRequested
} finally {
    if ($null -ne $RestartSignal) { $RestartSignal.Dispose() }
    foreach ($Name in $Variables.Keys) { [Environment]::SetEnvironmentVariable($Name, $Previous[$Name], 'Process') }
    foreach ($Name in $AdminNames) { $AdminEnvironment[$Name] = $null; $Variables[$Name] = $null }; foreach ($Name in @($ResolvedAdmin.Keys)) { $ResolvedAdmin[$Name] = $null }
    Pop-Location
}
