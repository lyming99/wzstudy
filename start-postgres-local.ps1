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

# Canonical local Windows PostgreSQL implementation. Generated configuration is
# saved only in .env; existing credentials and PostgreSQL volumes are never reset.
# The API stays in the foreground.
$ErrorActionPreference = 'Stop'
$Root = [IO.Path]::GetFullPath($PSScriptRoot)
$LocalRoot = Join-Path $Root '.local'
$Profile = Join-Path $LocalRoot 'postgres-local'
$CredentialFile = Join-Path $Profile 'connection.json'
$AdminFile = Join-Path $Profile 'admin-initial.json'
$MarkerFile = Join-Path $Profile 'volume-created.txt'
$PendingFile = Join-Path $Profile 'volume-pending.txt'
$ComposeFile = Join-Path $Root 'scripts\docker-compose-local-postgres.yml'
$ExplicitDatabasePort = $PSBoundParameters.ContainsKey('DatabasePort')
$ExplicitApiPort = $PSBoundParameters.ContainsKey('ApiPort') -or $PSBoundParameters.ContainsKey('Address')
$ExplicitAdminEmail = $PSBoundParameters.ContainsKey('AdministratorEmail')
# Listen on all IPv4 adapters by default so trusted LAN devices can reach the
# workspace. Use -Address 127.0.0.1:<port> for loopback-only access.
$ApiHost = '0.0.0.0'
if ($Lan -and $PSBoundParameters.ContainsKey('Address')) {
    throw 'Use either -Lan or -Address, not both.'
}
if ($PSBoundParameters.ContainsKey('Address')) {
    if ($PSBoundParameters.ContainsKey('ApiPort')) { throw 'Use either -Address or -ApiPort, not both.' }
    if ($Address -notmatch '^([^:]+):(\d{1,5})$') { throw 'Use -Address in IPv4:port form, for example 127.0.0.1:8091.' }
    $parsedIp = $null
    if (-not [Net.IPAddress]::TryParse($Matches[1], [ref]$parsedIp) -or $parsedIp.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
        throw 'The -Address host must be a local IPv4 address.'
    }
    $ApiHost = $parsedIp.ToString()
    $ApiPort = [int]$Matches[2]
    if ($ApiPort -lt 1 -or $ApiPort -gt 65535) { throw 'The service port must be between 1 and 65535.' }
}
if ($InitializeAdmin) { throw '-InitializeAdmin is no longer supported. Configure SYSTEM_ADMIN_EMAIL and SYSTEM_ADMIN_PASSWORD, or use -AdministratorEmail with a generated password.' }
if (-not [string]::IsNullOrWhiteSpace($ProfileDirectory)) { throw '-ProfileDirectory was for file storage. PostgreSQL uses the existing .local/postgres-local profile; no file-mode workspace is selected or migrated.' }

# The attachment path is resolved here. Administrator settings are read only
# if the database needs initialization, and are never printed or passed to Docker.
if ([string]::IsNullOrWhiteSpace($DataDirectory)) { $DataDirectory = [Environment]::GetEnvironmentVariable('DATA_DIR', 'Process') }
$EnvPath = Join-Path $Root '.env'
if ([string]::IsNullOrWhiteSpace($DataDirectory) -and -not $NoEnvFile -and (Test-Path -LiteralPath $EnvPath -PathType Leaf)) {
    $foundDataDirectory = $false
    foreach ($line in Get-Content -LiteralPath $EnvPath -Encoding UTF8) {
        if ($line -match '^\s*DATA_DIR\s*=(.*)$') {
            if ($foundDataDirectory) { throw 'DATA_DIR appears more than once in .env. Remove the duplicate before starting.' }
            $foundDataDirectory = $true
            $DataDirectory = $Matches[1].Trim()
            if ($DataDirectory.Length -ge 2 -and (($DataDirectory.StartsWith('"') -and $DataDirectory.EndsWith('"')) -or ($DataDirectory.StartsWith("'") -and $DataDirectory.EndsWith("'")))) {
                $DataDirectory = $DataDirectory.Substring(1, $DataDirectory.Length - 2)
            }
        }
    }
}
if ([string]::IsNullOrWhiteSpace($DataDirectory)) { $DataDirectory = Join-Path $Profile 'data' }
elseif (-not [IO.Path]::IsPathRooted($DataDirectory)) { $DataDirectory = Join-Path $Root $DataDirectory }
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory)

if ($Check) {
    [PSCustomObject]@{
        project_root = $Root
        profile_directory = $Profile
        data_directory = $DataDirectory
        configuration_file = $EnvPath
        configuration_exists = (Test-Path -LiteralPath $EnvPath -PathType Leaf)
        requested_database_port = $DatabasePort
        api_port = $ApiPort
        address = "${ApiHost}:$ApiPort"
        database_host = '127.0.0.1'
        api_host = $ApiHost
        store = 'postgres'
    } | ConvertTo-Json -Compress
    return
}
if ($env:OS -ne 'Windows_NT') { throw 'This launcher requires Windows PowerShell and Windows file ACLs.' }
. (Join-Path $Root 'scripts\local-service-lifecycle.ps1')
. (Join-Path $Root 'scripts\local-env.ps1')
. (Join-Path $Root 'scripts\local-postgres-state.ps1')
if (-not (Test-Path -LiteralPath $ComposeFile -PathType Leaf)) { throw 'The local PostgreSQL Compose file is missing.' }
$Go = Get-Command go -ErrorAction SilentlyContinue
$SourceAvailable = Test-Path -LiteralPath (Join-Path $Root 'go.mod') -PathType Leaf
$BuildFromSource = $SourceAvailable -and $null -ne $Go -and -not $SkipBuild
$PackagedMigration = Join-Path $Root 'dist\wzstudy-migrate.exe'
$PackagedApi = Join-Path $Root 'dist\wzstudy.exe'
if (-not (Test-Path -LiteralPath $PackagedApi -PathType Leaf)) {
    $legacyApi = Get-ChildItem -LiteralPath (Join-Path $Root 'dist') -Filter 'wzstudy-v*-windows-amd64.exe' -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1
    if ($null -ne $legacyApi) { $PackagedApi = $legacyApi.FullName }
}
if (-not $BuildFromSource -and (-not (Test-Path -LiteralPath $PackagedMigration -PathType Leaf) -or -not (Test-Path -LiteralPath $PackagedApi -PathType Leaf))) {
    throw 'Go 1.25+ and the source project, or both packaged API and migration executables, are required. PostgreSQL is mandatory; no file-mode service was started.'
}
$Docker = Get-Command docker -ErrorAction SilentlyContinue
if (-not $Docker) { throw 'Docker CLI and Docker Compose are required. Install Docker Desktop first.' }
$DockerPath = $Docker.Source

function Invoke-Docker([string[]]$Arguments, [AllowNull()][string]$InputText = $null) {
    $oldPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        if ($null -eq $InputText) { $output = @(& $DockerPath @Arguments 2>&1) }
        else { $output = @($InputText | & $DockerPath @Arguments 2>&1) }
        $code = $LASTEXITCODE
    } finally { $ErrorActionPreference = $oldPreference }
    return [PSCustomObject]@{ Code = $code; Text = (($output | ForEach-Object { $_.ToString() }) -join "`n") }
}
function Assert-LocalDockerEndpoint {
    $endpoint = [Environment]::GetEnvironmentVariable('DOCKER_HOST', 'Process')
    $explicitContext = [Environment]::GetEnvironmentVariable('DOCKER_CONTEXT', 'Process')
    if ($endpoint -and $explicitContext) { throw 'DOCKER_HOST and DOCKER_CONTEXT are both set; refusing an ambiguous Docker target.' }
    if (-not $endpoint) {
        $current = Invoke-Docker @('context', 'show')
        if ($current.Code -ne 0 -or -not $current.Text.Trim()) { throw 'Could not determine the Docker context; no database was started.' }
        $inspection = Invoke-Docker @('context', 'inspect', $current.Text.Trim())
        if ($inspection.Code -ne 0) { throw 'Could not inspect the Docker context; no database was started.' }
        try { $contexts = @($inspection.Text | ConvertFrom-Json) } catch { throw 'Invalid Docker context metadata; no database was started.' }
        if ($contexts.Count -ne 1) { throw 'Unexpected Docker context metadata; no database was started.' }
        $endpoint = [string]$contexts[0].Endpoints.docker.Host
    }
    # Only local named-pipe or Unix-socket engines are accepted. A TCP/SSH
    # context may be remote or tunnelled, so never operate on it implicitly.
    if ($endpoint -notmatch '(?i)^npipe:////\./pipe/[a-z0-9_.-]+$' -and $endpoint -notmatch '^unix:///[^\s]+$') {
        throw 'This launcher requires a local Docker named-pipe or Unix-socket context. Remote/TCP Docker contexts are not used.'
    }
}
function Test-FreeLoopbackPort([int]$Port) {
    $listener = $null
    try {
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
        $listener.ExclusiveAddressUse = $true
        $listener.Start()
        return $true
    } catch { return $false }
    finally { if ($listener) { $listener.Stop() } }
}
function Test-FreeApiPort([int]$Port) {
    $listener = $null
    try {
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Parse($ApiHost), $Port)
        $listener.ExclusiveAddressUse = $true
        $listener.Start()
        return $true
    } catch { return $false }
    finally { if ($listener) { $listener.Stop() } }
}
function Find-FreeLoopbackPort([int]$Start, [int]$Excluded = 0) {
    for ($candidate = $Start; $candidate -le [Math]::Min(65535, $Start + 20); $candidate++) {
        if ($candidate -ne $Excluded -and (Test-FreeLoopbackPort $candidate)) { return $candidate }
    }
    return 0
}
function Find-FreeApiPort([int]$Start, [int]$Excluded = 0) {
    for ($candidate = $Start; $candidate -le [Math]::Min(65535, $Start + 20); $candidate++) {
        if ($candidate -ne $Excluded -and (Test-FreeApiPort $candidate)) { return $candidate }
    }
    return 0
}
function New-RandomSecret {
    $bytes = New-Object byte[] 36
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}
function Test-LocalPostgresPassword([string]$Value) {
    if ([string]::IsNullOrEmpty($Value) -or $Value.IndexOfAny([char[]]@([char]0, [char]10, [char]13)) -ge 0) { return $false }
    try { $length = [Text.UTF8Encoding]::new($false, $true).GetByteCount($Value) } catch { return $false }
    return $length -ge 1 -and $length -le 256
}
function ConvertFrom-LocalPostgresUriComponent([string]$Value) {
    $utf8 = [Text.UTF8Encoding]::new($false, $true)
    $bytes = [Collections.Generic.List[byte]]::new()
    try {
        for ($index = 0; $index -lt $Value.Length;) {
            if ($Value[$index] -eq '%') {
                if ($index + 2 -ge $Value.Length -or $Value.Substring($index + 1, 2) -notmatch '^[a-fA-F0-9]{2}$') { throw 'invalid encoding' }
                $bytes.Add([Convert]::ToByte($Value.Substring($index + 1, 2), 16))
                $index += 3
            } else {
                $next = $Value.IndexOf('%', $index)
                if ($next -lt 0) { $next = $Value.Length }
                $bytes.AddRange($utf8.GetBytes($Value.Substring($index, $next - $index)))
                $index = $next
            }
        }
        return $utf8.GetString($bytes.ToArray())
    } catch { throw 'DATABASE_URL must use valid UTF-8 and percent-encoding in its credentials and database name.' }
}
function Read-LocalPostgresConfiguration([hashtable]$Values) {
    $configuredUrl = ([string]$Values['DATABASE_URL']).Trim()
    $configuredPassword = [string]$Values['POSTGRES_PASSWORD']
    $result = @{ HasUrl = $false; UrlHasPassword = $false; User = 'wzstudy'; Database = 'wzstudy'; Port = 0; Password = $configuredPassword }
    if ($configuredUrl) {
        try { $uri = [Uri]::new($configuredUrl, [UriKind]::Absolute) }
        catch { throw 'DATABASE_URL must be a valid local PostgreSQL URL.' }
        if ($uri.Scheme -notin @('postgres', 'postgresql') -or $uri.Host -notin @('127.0.0.1', 'localhost') -or
            $uri.Fragment -or ($uri.Query -and $uri.Query -cne '?sslmode=disable') -or
            -not $uri.UserInfo -or $uri.UserInfo -match '%(?![a-fA-F0-9]{2})' -or $uri.AbsolutePath -notmatch '^/[^/]+$' -or $uri.AbsolutePath -match '%(?![a-fA-F0-9]{2})') {
            throw 'DATABASE_URL for the local Docker launcher must use a local PostgreSQL URL with sslmode=disable and no additional options.'
        }
        $userParts = $uri.UserInfo -split ':', 2
        $result.User = ConvertFrom-LocalPostgresUriComponent $userParts[0]
        $result.Database = ConvertFrom-LocalPostgresUriComponent ($uri.AbsolutePath.Substring(1))
        $result.Port = if ($uri.Port -eq -1) { 5432 } else { $uri.Port }
        if ($result.User -notmatch '^[A-Za-z_][A-Za-z0-9_]{0,62}$' -or $result.Database -notmatch '^[A-Za-z_][A-Za-z0-9_]{0,62}$' -or
            $result.Port -lt 1 -or $result.Port -gt 65535) {
            throw 'DATABASE_URL must specify a valid database user, database name and local port.'
        }
        $urlPassword = if ($userParts.Length -eq 2) { ConvertFrom-LocalPostgresUriComponent $userParts[1] } else { '' }
        if ($urlPassword -and $configuredPassword -and $urlPassword -cne $configuredPassword) {
            throw 'DATABASE_URL and POSTGRES_PASSWORD specify different passwords. No database credentials were changed.'
        }
        if ($urlPassword) { $result.Password = $urlPassword; $result.UrlHasPassword = $true }
        $result.HasUrl = $true
    }
    if ($result.Password -and -not (Test-LocalPostgresPassword $result.Password)) {
        throw 'The configured PostgreSQL password must be 1-256 UTF-8 bytes without NUL or line breaks.'
    }
    return $result
}
function New-LocalPostgresUrl([string]$User, [string]$Password, [int]$Port, [string]$Database) {
    return 'postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable' -f $User, [Uri]::EscapeDataString($Password), $Port, $Database
}
function Get-CurrentSid { return [Security.Principal.WindowsIdentity]::GetCurrent().User }
function Read-PrivateJson([string]$Path) {
    $sid = (Get-CurrentSid).Value
    $acl = Get-Acl -LiteralPath $Path
    if (-not $acl.AreAccessRulesProtected -or $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid) {
        throw 'The local credentials file does not have the expected private owner/ACL. Restore its protection before retrying.'
    }
    foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
        if ($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -ne $sid) {
            throw 'The local credentials file grants access to another identity. Restrict it before retrying.'
        }
    }
    try { return (Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json) }
    catch { throw 'The local credentials file is invalid. Restore the original file; it will not be regenerated.' }
}
function Assert-PlainDirectory([string]$Path) {
    if (Test-Path -LiteralPath $Path) {
        $item = Get-Item -LiteralPath $Path -Force
        if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Unsafe local profile directory: $Path" }
    } else { New-Item -ItemType Directory -Path $Path -ErrorAction Stop | Out-Null }
}
function Get-ProjectSuffix([string]$Path) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes([IO.Path]::GetFullPath($Path).ToLowerInvariant())
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-', '').Substring(0, 12).ToLowerInvariant()
    } finally { $sha.Dispose() }
}
function Test-VolumeExists([string]$Name) {
    $result = Invoke-Docker @('volume', 'ls', '--filter', "name=$Name", '--format', '{{.Name}}')
    if ($result.Code -ne 0) { throw 'Could not inspect Docker volumes. No database was started.' }
    return [bool]@($result.Text -split "`r?`n" | Where-Object { $_.Trim() -eq $Name }).Count
}
function Assert-VolumeOwner([string]$Name, [string]$InstanceId) {
    $result = Invoke-Docker @('volume', 'inspect', $Name)
    if ($result.Code -ne 0) { throw 'Could not inspect the local PostgreSQL volume.' }
    try { $volumes = @($result.Text | ConvertFrom-Json) } catch { throw 'Docker returned invalid volume metadata.' }
    if ($volumes.Count -ne 1 -or $volumes[0].Labels.'org.wzstudy.local-instance' -ne $InstanceId) {
        throw 'The existing PostgreSQL volume does not belong to this saved profile. It will not be mounted or replaced.'
    }
}
function Assert-DatabasePort([int]$Port, [string]$ProjectName) {
    if (Test-FreeLoopbackPort $Port) { return }
    $result = Invoke-Docker @('ps', '--filter', "label=com.docker.compose.project=$ProjectName", '--filter', 'label=com.docker.compose.service=postgres', '--format', '{{.ID}}')
    if ($result.Code -ne 0) { throw 'Could not inspect the local PostgreSQL container.' }
    $ids = @($result.Text -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    if ($ids.Count -ne 1) { throw "Database port $Port is already occupied. No process was stopped; use another port only for a new profile." }
    $mapped = Invoke-Docker @('port', $ids[0], '5432/tcp')
    if ($mapped.Code -ne 0 -or @($mapped.Text -split "`r?`n" | Where-Object { $_.Trim() -eq "127.0.0.1:$Port" }).Count -ne 1) {
        throw "Database port $Port is not owned by this project's local PostgreSQL container. No process was stopped."
    }
}

if (-not (Test-FreeApiPort $ApiPort)) {
    if ($ExplicitApiPort) { throw "Service address ${ApiHost}:$ApiPort is unavailable or already occupied. Choose another -Address or -ApiPort; no process was stopped." }
    $availableApiPort = Find-FreeApiPort ($ApiPort + 1) $DatabasePort
    if (-not $availableApiPort) { throw 'No free service port was found near 8091. Choose -ApiPort explicitly; no process was stopped.' }
    Write-Host "Service port $ApiPort is occupied; using ${ApiHost}:$availableApiPort instead. Existing services are untouched."
    $ApiPort = $availableApiPort
}
$Lock = $null
$Mutex = $null
$MutexOwned = $false
$BuildDir = $null
$ResolvedAdmin = $null
$DatabaseSettings = $null
$EnvSnapshot = $null
$AdminNames = @('SYSTEM_ADMIN_EMAIL','SYSTEM_ADMIN_PASSWORD','SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME','BOOTSTRAP_ADMIN_EMAIL','BOOTSTRAP_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')
$Names = @('APP_ENV','STORE','HTTP_ADDR','DATA_DIR','DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY','CLIENT_KEY_ENCRYPTION_KEY','POLL_ENABLED','CORS_ORIGINS','WZSTUDY_LOCAL_INSTANCE_ID','WZSTUDY_LOCAL_VOLUME_STATE','WZSTUDY_LOCAL_PG_PASSWORD','WZSTUDY_LOCAL_PG_PORT','WZSTUDY_LOCAL_PG_VOLUME','WZSTUDY_LOCAL_PG_USER','WZSTUDY_LOCAL_PG_DATABASE') + $AdminNames
$Previous = @{}
foreach ($name in $Names) { $Previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    $suffix = Get-ProjectSuffix $Root
    $ProjectName = "wzstudy-local-$suffix"
    $VolumeName = "$ProjectName-pgdata"
    $Mutex = [Threading.Mutex]::new($false, "Global\WzStudyLocalPostgres-$suffix")
    try { $MutexOwned = $Mutex.WaitOne([TimeSpan]::FromSeconds(15)) }
    catch [Threading.AbandonedMutexException] { $MutexOwned = $true }
    if (-not $MutexOwned) { throw 'Another local PostgreSQL launcher is already running for this project.' }
    # A legacy launcher uses a file lock. Hold it as well while migrating or
    # while a retained legacy credentials file exists; new installations need no lock file.
    if (Test-Path -LiteralPath $CredentialFile -PathType Leaf) {
        Assert-PlainDirectory $LocalRoot
        Assert-PlainDirectory $Profile
        try { $Lock = [IO.File]::Open((Join-Path $Profile 'startup.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
        catch { throw 'A legacy local PostgreSQL launcher is already running; stop it before migration.' }
    }
    $EnvSnapshot = Read-LocalEnvSnapshot $EnvPath -ProcessOnly:$NoEnvFile
    $DatabaseSettings = Read-LocalPostgresConfiguration $EnvSnapshot.Values
    if ($DatabaseSettings.HasUrl) {
        if ($ExplicitDatabasePort -and $DatabasePort -ne $DatabaseSettings.Port) { throw 'DATABASE_URL and -DatabasePort specify different ports. No database credentials were changed.' }
        $DatabasePort = $DatabaseSettings.Port
    }
    # Child processes do not inherit administrator bootstrap credentials.
    foreach ($name in $AdminNames) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') }
    foreach ($name in @('DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY','CLIENT_KEY_ENCRYPTION_KEY')) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') }
    Assert-LocalDockerEndpoint
    if ((Invoke-Docker @('version', '--format', '{{.Server.Version}}')).Code -ne 0) { throw 'Docker engine is not running. Start Docker Desktop and try again.' }
    if ((Invoke-Docker @('compose', 'version', '--short')).Code -ne 0) { throw 'Docker Compose v2 is required.' }
    if (-not $NoEnvFile) { Protect-LocalEnv $EnvSnapshot }
    Assert-PlainDirectory $LocalRoot
    Assert-PlainDirectory $Profile
    $LocalConfig = Initialize-LocalPostgresConfiguration -Snapshot $EnvSnapshot -DatabaseSettings $DatabaseSettings -RequestedPort $DatabasePort -RequestedApiPort $ApiPort -ExplicitPort $ExplicitDatabasePort -ExplicitApi $ExplicitApiPort -ProjectName $ProjectName -VolumeName $VolumeName -LegacyCredentialFile $CredentialFile -LegacyMarkerFile $MarkerFile -LegacyPendingFile $PendingFile
    $EnvSnapshot = $LocalConfig.Snapshot
    $Credentials = $LocalConfig.Credentials
    $VolumeExists = $LocalConfig.VolumeExists
    $VolumeState = $LocalConfig.State
    $DatabasePort = $LocalConfig.DatabasePort
    $ApiPort = $LocalConfig.ApiPort

    $BuildDir = Join-Path $Profile ('.build-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $BuildDir -ErrorAction Stop | Out-Null
    $MigrationExe = Join-Path $BuildDir 'migrate.exe'
    $ApiExe = Join-Path $BuildDir 'wzstudy-api.exe'
    if ($BuildFromSource) {
        Push-Location $Root
        try {
            Write-Host 'Building the service and database migration tool...'
            & $Go.Source build -trimpath -o $MigrationExe ./cmd/migrate
            if ($LASTEXITCODE -ne 0) { throw 'Migration tool build failed. The existing database was not changed.' }
            & $Go.Source build -trimpath -o $ApiExe ./cmd/api
            if ($LASTEXITCODE -ne 0) { throw 'Service build failed. The existing database was not changed.' }
        } finally { Pop-Location }
    } else {
        Write-Host 'Using the packaged service and database migration tool...'
        Copy-Item -LiteralPath $PackagedMigration -Destination $MigrationExe -ErrorAction Stop
        Copy-Item -LiteralPath $PackagedApi -Destination $ApiExe -ErrorAction Stop
    }
    if (-not $VolumeExists) {
        $created = Invoke-Docker @('volume', 'create', '--label', "org.wzstudy.local-instance=$($Credentials.instance_id)", $VolumeName)
        if ($created.Code -ne 0) { throw 'Could not create the dedicated PostgreSQL volume.' }
        Assert-VolumeOwner $VolumeName $Credentials.instance_id
    }
    if ($VolumeState -ceq 'pending') {
        # Compose must not start until the created state is durably in .env.
        $EnvSnapshot = Save-LocalEnvSnapshot $EnvSnapshot @{ WZSTUDY_LOCAL_VOLUME_STATE='created' }
        $VolumeState = 'created'
    }

    Assert-LocalEnvUnchanged $EnvSnapshot
    $env:WZSTUDY_LOCAL_PG_PASSWORD = $Credentials.database_password
    $env:WZSTUDY_LOCAL_PG_PORT = [string]$DatabasePort
    $env:WZSTUDY_LOCAL_PG_VOLUME = $VolumeName
    $env:WZSTUDY_LOCAL_PG_USER = $Credentials.database_user
    $env:WZSTUDY_LOCAL_PG_DATABASE = $Credentials.database_name
    $env:DATABASE_URL = $Credentials.database_url
    $env:ADMIN_API_KEY = $Credentials.admin_api_key
    $env:READ_API_KEY = $Credentials.read_api_key
    [Environment]::SetEnvironmentVariable('CLIENT_KEY_ENCRYPTION_KEY', [string]$EnvSnapshot.Values['CLIENT_KEY_ENCRYPTION_KEY'], 'Process')
    $env:STORE = 'postgres'
    $env:APP_ENV = 'development'
    $env:HTTP_ADDR = "${ApiHost}:$ApiPort"
    $env:DATA_DIR = $DataDirectory
    $env:POLL_ENABLED = 'true'
    $env:CORS_ORIGINS = ''
    foreach ($name in $AdminNames) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') }
    $EmptyEnv = Join-Path $BuildDir 'compose.env'
    [IO.File]::WriteAllText($EmptyEnv, '# Values are supplied by the launcher.', [Text.UTF8Encoding]::new($false))
    $Compose = @('compose','--project-directory',$Root,'--env-file',$EmptyEnv,'-p',$ProjectName,'-f',$ComposeFile)
    Write-Host "Starting the dedicated Docker PostgreSQL on 127.0.0.1:$DatabasePort..."
    $started = Invoke-Docker ($Compose + @('up','-d','postgres'))
    if ($started.Code -ne 0) { throw 'Docker could not start the dedicated PostgreSQL container. Check Docker Desktop, image access and the database port; no data volume was deleted.' }
    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $ready = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        $probe = Invoke-Docker ($Compose + @('exec','-T','postgres','pg_isready','-q','-h','127.0.0.1','-U',[string]$Credentials.database_user,'-d',[string]$Credentials.database_name))
        if ($probe.Code -eq 0) { $ready = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $ready) { throw 'PostgreSQL did not become ready before the timeout. The data volume and credentials were retained.' }
    Write-Host 'PostgreSQL is ready; applying database migrations...'
    & $MigrationExe
    if ($LASTEXITCODE -ne 0) { throw 'Database connection or migration failed. Check the saved credentials; the data volume was not reset.' }

    $query = "SELECT EXISTS (SELECT 1 FROM workspace_records WHERE kind='system_administrator' AND id='singleton');"
    $shell = 'export PGPASSWORD=$POSTGRES_PASSWORD; exec psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -U $POSTGRES_USER -d $POSTGRES_DB -A -t'
    $adminResult = Invoke-Docker ($Compose + @('exec','-T','postgres','sh','-c',$shell)) $query
    if ($adminResult.Code -ne 0) { throw 'Could not check administrator initialization; existing database data was not changed.' }
    $flags = @($adminResult.Text -split "`r?`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -in @('t','f') })
    if ($flags.Count -ne 1) { throw 'Unexpected administrator initialization status; refusing to guess.' }
    if ($flags[0] -eq 'f') {
        Assert-LocalEnvUnchanged $EnvSnapshot
        $AdminGroups = [ordered]@{
            SYSTEM_ADMIN_EMAIL = @('SYSTEM_ADMIN_EMAIL','BOOTSTRAP_ADMIN_EMAIL')
            SYSTEM_ADMIN_PASSWORD = @('SYSTEM_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_PASSWORD')
            SYSTEM_ADMIN_DISPLAY_NAME = @('SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')
        }
        $ResolvedAdmin = @{}
        foreach ($canonical in $AdminGroups.Keys) {
            $ResolvedAdmin[$canonical] = $null
            foreach ($name in $AdminGroups[$canonical]) {
                $value = [string]$EnvSnapshot.Values[$name]
                if ($canonical -ne 'SYSTEM_ADMIN_PASSWORD') { $value = $value.Trim() }
                if ($value -ne '') { $ResolvedAdmin[$canonical] = $value; break }
            }
        }
        $configuredEmail = [string]$ResolvedAdmin['SYSTEM_ADMIN_EMAIL']
        $configuredPassword = [string]$ResolvedAdmin['SYSTEM_ADMIN_PASSWORD']
        $hasConfiguredEmail = -not [string]::IsNullOrWhiteSpace($configuredEmail)
        $hasConfiguredPassword = -not [string]::IsNullOrEmpty($configuredPassword)
        if ($hasConfiguredEmail -and -not $hasConfiguredPassword) { throw 'SYSTEM_ADMIN_EMAIL is configured without SYSTEM_ADMIN_PASSWORD. Set both or leave both empty to use a generated password.' }
        if ($hasConfiguredPassword -and -not $hasConfiguredEmail -and -not $ExplicitAdminEmail) { throw 'SYSTEM_ADMIN_PASSWORD is configured without SYSTEM_ADMIN_EMAIL. Set both, or supply -AdministratorEmail.' }
        if ($hasConfiguredPassword) {
            try { $passwordBytes = [Text.UTF8Encoding]::new($false, $true).GetByteCount($configuredPassword) }
            catch { throw 'SYSTEM_ADMIN_PASSWORD must be valid UTF-8.' }
            if ($passwordBytes -lt 8 -or $passwordBytes -gt 128 -or $configuredPassword.Contains([char]0)) { throw 'SYSTEM_ADMIN_PASSWORD must contain 8-128 UTF-8 bytes without NUL.' }
        }
        $email = if ($ExplicitAdminEmail) { $AdministratorEmail.Trim().ToLowerInvariant() }
                 elseif ($hasConfiguredEmail) { $configuredEmail.Trim().ToLowerInvariant() }
                 else { $AdministratorEmail.Trim().ToLowerInvariant() }
        try { $parsed = [Net.Mail.MailAddress]::new($email) } catch { throw 'The first administrator email must be a valid email address.' }
        if ($parsed.Address -cne $email -or $email.Length -gt 254) { throw 'The first administrator email must be a valid email address.' }
        $InitialAdmin = $null
        if (Test-Path -LiteralPath $AdminFile -PathType Leaf) {
            $InitialAdmin = Read-PrivateJson $AdminFile
            if (-not $InitialAdmin.email -or -not $InitialAdmin.password) { throw 'The saved legacy initial administrator credentials are invalid; restore them before retrying.' }
            if ($hasConfiguredPassword -and ($InitialAdmin.email -cne $email -or $InitialAdmin.password -cne $configuredPassword)) {
                throw 'Configured administrator credentials conflict with the saved legacy initial-password file. Both were retained; resolve the conflict before retrying.'
            }
            if ($ExplicitAdminEmail -and $InitialAdmin.email -cne $email) { throw 'The requested administrator email differs from the saved legacy initial credentials; they were not changed.' }
        }
        if (-not $hasConfiguredPassword) {
            if ($NoEnvFile) { throw '-NoEnvFile requires existing administrator credentials in process environment; use .env to generate them.' }
            if (-not $InitialAdmin) { $InitialAdmin = [PSCustomObject]@{ email=$email; password=New-RandomSecret } }
            $email = [string]$InitialAdmin.email
            $configuredPassword = [string]$InitialAdmin.password
            $EnvSnapshot = Save-LocalEnvSnapshot $EnvSnapshot @{ SYSTEM_ADMIN_EMAIL=$email; SYSTEM_ADMIN_PASSWORD=$configuredPassword }
            Write-Host 'The initial administrator credentials were saved in .env; the password is not printed.'
        } elseif (-not $NoEnvFile -and ($ExplicitAdminEmail -or -not $hasConfiguredEmail) -and $configuredEmail.Trim().ToLowerInvariant() -cne $email) {
            $EnvSnapshot = Save-LocalEnvSnapshot $EnvSnapshot @{ SYSTEM_ADMIN_EMAIL=$email }
        }
        $env:SYSTEM_ADMIN_EMAIL = $email
        $env:SYSTEM_ADMIN_PASSWORD = $configuredPassword
        if ($ResolvedAdmin['SYSTEM_ADMIN_DISPLAY_NAME']) { $env:SYSTEM_ADMIN_DISPLAY_NAME = $ResolvedAdmin['SYSTEM_ADMIN_DISPLAY_NAME'] }
        Write-Host "First administrator email: $email"
    } else {
        # Keep the configured administrator email available for server-side
        # role checks. The service rejects a mismatch; never reset its password.
        Assert-LocalEnvUnchanged $EnvSnapshot
        $existingEmail = if ($ExplicitAdminEmail) { $AdministratorEmail.Trim() }
                         elseif (-not [string]::IsNullOrWhiteSpace([string]$EnvSnapshot.Values['SYSTEM_ADMIN_EMAIL'])) { ([string]$EnvSnapshot.Values['SYSTEM_ADMIN_EMAIL']).Trim() }
                         else { ([string]$EnvSnapshot.Values['BOOTSTRAP_ADMIN_EMAIL']).Trim() }
        if ($existingEmail) { $env:SYSTEM_ADMIN_EMAIL = $existingEmail }
        Write-Host 'Existing PostgreSQL administrator retained; no password was reset. The service verifies any configured administrator email.'
    }

    $BrowserHost = if ($ApiHost -eq '0.0.0.0') { '127.0.0.1' } else { $ApiHost }
    Write-Host "Workspace address after startup: http://${BrowserHost}:$ApiPort/workspace/"
    if ($ApiHost -notmatch '^127\.') {
        Write-Warning 'LAN access exposes this API to the network. HTTP sends credentials without encryption; use only a trusted network and prefer HTTPS. No firewall rules were changed.'
        if ($ApiHost -eq '0.0.0.0') {
            Write-Host "Listening address requested: 0.0.0.0:$ApiPort (all IPv4 interfaces; the OS may also allow IPv6 via a dual-stack socket)."
            Write-Host 'Candidate service origins for the QR code (choose the interface reachable from your phone):'
            $lanOrigins = @()
            try {
                foreach ($adapter in [Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces()) {
                    if ($adapter.OperationalStatus -ne [Net.NetworkInformation.OperationalStatus]::Up -or
                        $adapter.NetworkInterfaceType -eq [Net.NetworkInformation.NetworkInterfaceType]::Loopback) { continue }
                    foreach ($unicast in $adapter.GetIPProperties().UnicastAddresses) {
                        $ip = $unicast.Address
                        if ($ip.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or
                            [Net.IPAddress]::IsLoopback($ip) -or $ip.ToString() -like '169.254.*' -or
                            $ip.Equals([Net.IPAddress]::Any)) { continue }
                        $label = $adapter.Name -replace '[\r\n\t]', ' '
                        $lanOrigins += "  http://$($ip.ToString()):$ApiPort  ($label)"
                    }
                }
            } catch { Write-Warning 'Could not enumerate network interfaces; check your IPv4 address manually with ipconfig.' }
            if ($lanOrigins.Count) { $lanOrigins | Select-Object -Unique | ForEach-Object { Write-Host $_ } }
            else { Write-Host '  No active IPv4 address found; check ipconfig after connecting to a network.' }
        } else {
            Write-Host "Service origin for the QR code: http://${ApiHost}:$ApiPort"
        }
        Write-Host 'Use the phone-reachable address, not 0.0.0.0 or 127.0.0.1. Check Windows Firewall and Wi-Fi client isolation if the phone cannot connect.'
    }
    Write-Host "PostgreSQL: 127.0.0.1:$DatabasePort / database $($Credentials.database_name) / user $($Credentials.database_user)"
    Write-Host "Docker data volume: $VolumeName"
    Write-Host "Media data directory: $DataDirectory"
    if ($NoEnvFile) { Write-Host 'Database/API configuration was supplied by the process environment.' }
    else { Write-Host "Database/API configuration and generated credentials: $EnvPath" }
    Write-Host 'Existing file-mode data was not migrated. Keep this terminal open; Ctrl+C stops the API, not the Docker database.'
    $RestartSignal = New-LocalRestartSignal -Port $ApiPort
    Push-Location $Root
    try {
        & $ApiExe
        $ServiceExitCode = $LASTEXITCODE
        $RestartWasRequested = $RestartSignal.WaitOne(0)
        Assert-LocalServiceExit -ExitCode $ServiceExitCode -ServiceAddress "${BrowserHost}:$ApiPort" -RestartRequested $RestartWasRequested
    } finally {
        Pop-Location
        $RestartSignal.Dispose()
    }
} finally {
    foreach ($name in $Names) { [Environment]::SetEnvironmentVariable($name, $Previous[$name], 'Process') }
    if ($ResolvedAdmin) { foreach ($name in @($ResolvedAdmin.Keys)) { $ResolvedAdmin[$name] = $null } }
    if ($DatabaseSettings) { $DatabaseSettings.Password = $null }
    $databasePassword = $null
    $configuredPassword = $null
    $InitialAdmin = $null
    if ($BuildDir -and (Test-Path -LiteralPath $BuildDir)) { Remove-Item -LiteralPath $BuildDir -Recurse -Force -ErrorAction SilentlyContinue }
    if ($Lock) { $Lock.Dispose() }
    if ($MutexOwned -and $Mutex) { $Mutex.ReleaseMutex() }
    if ($Mutex) { $Mutex.Dispose() }
    $Credentials = $null
    $EnvSnapshot = $null
}
