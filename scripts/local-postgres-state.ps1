# Requires the local-env helpers and the launcher's Docker/crypto helpers.
# Returns in-memory credentials; .env is the only persistent configuration.
function Initialize-LocalPostgresConfiguration {
    param($Snapshot, $DatabaseSettings, [int]$RequestedPort, [int]$RequestedApiPort,
          [bool]$ExplicitPort, [bool]$ExplicitApi, [string]$ProjectName, [string]$VolumeName,
          [string]$LegacyCredentialFile, [string]$LegacyMarkerFile, [string]$LegacyPendingFile)
    $port = $RequestedPort; $apiPort = $RequestedApiPort
    $volumeExists = Test-VolumeExists $VolumeName
    $instance = [string]$Snapshot.Values['WZSTUDY_LOCAL_INSTANCE_ID']
    $state = [string]$Snapshot.Values['WZSTUDY_LOCAL_VOLUME_STATE']
    if ([bool]$instance -ne [bool]$state) { throw 'Local PostgreSQL identity/state is incomplete in .env. Restore the original configuration; no data was changed.' }
    if ($instance -and ($instance -cnotmatch '^[a-f0-9]{32}$' -or $state -cnotin @('pending','created'))) {
        throw 'Local PostgreSQL identity/state in .env is invalid. Restore the original configuration; no data was changed.'
    }
    if (-not $instance -and (Test-Path -LiteralPath $LegacyCredentialFile -PathType Leaf)) {
        if ($Snapshot.ProcessOnly) { throw 'Legacy PostgreSQL credentials must first be migrated to .env; omit -NoEnvFile.' }
        $legacy = Read-PrivateJson $LegacyCredentialFile
        if ($legacy.version -ne 1 -or $legacy.project -ne $ProjectName -or $legacy.volume -ne $VolumeName -or
            $legacy.database_host -ne '127.0.0.1' -or $legacy.database_user -notmatch '^[A-Za-z_][A-Za-z0-9_]{0,62}$' -or $legacy.database_name -notmatch '^[A-Za-z_][A-Za-z0-9_]{0,62}$' -or
            $legacy.instance_id -notmatch '^[a-f0-9]{32}$' -or -not (Test-LocalPostgresPassword $legacy.database_password) -or
            $legacy.admin_api_key -notmatch '^[A-Za-z0-9_-]{32,128}$' -or $legacy.read_api_key -notmatch '^[A-Za-z0-9_-]{32,128}$' -or
            $legacy.admin_api_key -ceq $legacy.read_api_key -or [int]$legacy.database_port -lt 1 -or [int]$legacy.database_port -gt 65535) {
            throw 'Legacy PostgreSQL credentials are invalid or belong to another project; restore them before migration.'
        }
        $legacyUrl = New-LocalPostgresUrl $legacy.database_user $legacy.database_password ([int]$legacy.database_port) $legacy.database_name
        if ($legacy.database_url -cne $legacyUrl) { throw 'The legacy database URL does not match its credentials. Restore the original file.' }
        if (($ExplicitPort -or $DatabaseSettings.HasUrl) -and $port -ne [int]$legacy.database_port) { throw 'The requested database port differs from the legacy configuration; no data was changed.' }
        if ($DatabaseSettings.HasUrl -and ($DatabaseSettings.User -cne $legacy.database_user -or $DatabaseSettings.Database -cne $legacy.database_name)) { throw 'DATABASE_URL conflicts with the legacy database user or name; no data was changed.' }
        if ($DatabaseSettings.Password -and $DatabaseSettings.Password -cne $legacy.database_password) { throw 'The database password conflicts with the legacy configuration; no data was changed.' }
        foreach ($pair in @(@('ADMIN_API_KEY','admin_api_key'), @('READ_API_KEY','read_api_key'))) {
            if ($Snapshot.Values[$pair[0]] -and $Snapshot.Values[$pair[0]] -cne $legacy.($pair[1])) { throw "$($pair[0]) conflicts with the legacy configuration; no data was changed." }
        }
        $hasMarker = Test-Path -LiteralPath $LegacyMarkerFile -PathType Leaf
        $hasPending = Test-Path -LiteralPath $LegacyPendingFile -PathType Leaf
        if ($hasMarker -and (Get-Content -LiteralPath $LegacyMarkerFile -Raw -Encoding UTF8).Trim() -cne $legacy.instance_id) { throw 'The legacy volume marker does not match the legacy credentials.' }
        if ($hasPending -and (Get-Content -LiteralPath $LegacyPendingFile -Raw -Encoding UTF8).Trim() -cne $legacy.instance_id) { throw 'The legacy pending marker does not match the legacy credentials.' }
        if (-not $hasMarker -and -not $hasPending) { throw 'The legacy volume initialization marker is missing. Restore it before migration.' }
        if ($hasMarker -and -not $volumeExists) { throw 'The legacy PostgreSQL data volume is missing. Restore it; no replacement was created.' }
        if ($volumeExists) { Assert-VolumeOwner $VolumeName $legacy.instance_id }
        $instance = [string]$legacy.instance_id
        $state = if ($hasMarker) { 'created' } else { 'pending' }
        $updates = @{
            POSTGRES_PASSWORD=[string]$legacy.database_password; ADMIN_API_KEY=[string]$legacy.admin_api_key; READ_API_KEY=[string]$legacy.read_api_key
            WZSTUDY_LOCAL_INSTANCE_ID=$instance; WZSTUDY_LOCAL_VOLUME_STATE=$state
        }
        if (-not $DatabaseSettings.HasUrl -or -not $DatabaseSettings.UrlHasPassword) { $updates.DATABASE_URL = $legacyUrl }
        $Snapshot = Save-LocalEnvSnapshot $Snapshot $updates
        $DatabaseSettings = Read-LocalPostgresConfiguration $Snapshot.Values
        $port = [int]$legacy.database_port
        Write-Host 'Legacy database settings were migrated to .env; the original files and data were retained.'
    }
    if (-not $instance) {
        if ($Snapshot.ProcessOnly) { throw '-NoEnvFile requires complete existing database identity and credentials in process environment; use .env for first initialization.' }
        if ($volumeExists -or (Test-Path -LiteralPath $LegacyMarkerFile) -or (Test-Path -LiteralPath $LegacyPendingFile)) {
            throw 'A PostgreSQL volume or legacy marker exists without recoverable configuration. Restore the original .env or legacy credentials; no data was replaced.'
        }
        if (-not $ExplicitPort -and -not $DatabaseSettings.HasUrl -and -not (Test-FreeLoopbackPort $port)) {
            $available = Find-FreeLoopbackPort ($port + 1) $apiPort
            if (-not $available) { throw 'No free local database port was found near 15432. Choose -DatabasePort explicitly.' }
            Write-Host "Database port $port is occupied; the new local profile will use 127.0.0.1:$available instead."
            $port = $available
        }
        $instance = [guid]::NewGuid().ToString('N')
        $state = 'pending'
    } else {
        if (-not $DatabaseSettings.HasUrl -or -not $DatabaseSettings.Password -or -not $Snapshot.Values['ADMIN_API_KEY'] -or -not $Snapshot.Values['READ_API_KEY']) {
            throw 'Existing PostgreSQL credentials are incomplete in .env. Restore the original configuration; no new password or data volume was created.'
        }
        if ($state -ceq 'created' -and -not $volumeExists) { throw 'The saved PostgreSQL data volume is missing. Restore it; the launcher will not create an empty replacement.' }
        if ($volumeExists) { Assert-VolumeOwner $VolumeName $instance }
    }
    if ($Snapshot.ProcessOnly -and ($state -cne 'created' -or -not $volumeExists)) {
        throw '-NoEnvFile can only use a previously created owned volume with complete process credentials; use .env for initialization.'
    }
    if ($port -eq $apiPort) {
        if ($ExplicitApi) { throw 'Database and service ports must be different.' }
        $available = Find-FreeApiPort ($apiPort + 1) $port
        if (-not $available) { throw 'No free service port was found; choose -ApiPort explicitly.' }
        Write-Host "The database uses port $port; the service will use ${ApiHost}:$available."
        $apiPort = $available
    }
    Assert-DatabasePort $port $ProjectName
    $password = if ($DatabaseSettings.Password) { $DatabaseSettings.Password } else { New-RandomSecret }
    $user = if ($DatabaseSettings.HasUrl) { $DatabaseSettings.User } else { 'wzstudy' }
    $database = if ($DatabaseSettings.HasUrl) { $DatabaseSettings.Database } else { 'wzstudy' }
    $url = New-LocalPostgresUrl $user $password $port $database
    $adminKey = [string]$Snapshot.Values['ADMIN_API_KEY']; $readKey = [string]$Snapshot.Values['READ_API_KEY']
    if (-not $adminKey) { $adminKey = New-RandomSecret }
    if (-not $readKey) { $readKey = New-RandomSecret }
    try {
        $utf8 = [Text.UTF8Encoding]::new($false, $true)
        $adminKeyLength = $utf8.GetByteCount($adminKey); $readKeyLength = $utf8.GetByteCount($readKey)
    } catch { throw 'API keys must be valid UTF-8.' }
    if ($adminKeyLength -lt 32 -or $adminKeyLength -gt 256 -or $readKeyLength -lt 32 -or $readKeyLength -gt 256 -or
        $adminKey -ceq $readKey -or $adminKey.Trim() -cne $adminKey -or $readKey.Trim() -cne $readKey) {
        throw 'ADMIN_API_KEY and READ_API_KEY must be distinct 32-256 UTF-8 byte secrets without surrounding whitespace.'
    }
    if (-not $Snapshot.ProcessOnly) {
        $updates = @{
            POSTGRES_PASSWORD=$password; ADMIN_API_KEY=$adminKey; READ_API_KEY=$readKey
            WZSTUDY_LOCAL_INSTANCE_ID=$instance; WZSTUDY_LOCAL_VOLUME_STATE=$state
        }
        if (-not $DatabaseSettings.HasUrl -or -not $DatabaseSettings.UrlHasPassword) { $updates.DATABASE_URL = $url }
        $Snapshot = Save-LocalEnvSnapshot $Snapshot $updates
    }
    $credentials = [PSCustomObject]@{
        project=$ProjectName; volume=$VolumeName; instance_id=$instance; database_host='127.0.0.1'; database_port=$port
        database_user=$user; database_name=$database; database_password=$password; database_url=$url
        admin_api_key=$adminKey; read_api_key=$readKey
    }
    return [PSCustomObject]@{ Snapshot=$Snapshot; Credentials=$credentials; State=$state; VolumeExists=$volumeExists; ApiPort=$apiPort; DatabasePort=$port }
}
