# Local launcher configuration is entirely in .env. The helpers do not echo
# values and keep plaintext temporary files private to the current Windows user.
$script:LocalEnvKeys = @('DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY',
    'WZSTUDY_LOCAL_INSTANCE_ID','WZSTUDY_LOCAL_VOLUME_STATE',
    'SYSTEM_ADMIN_EMAIL','SYSTEM_ADMIN_PASSWORD','SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME',
    'BOOTSTRAP_ADMIN_EMAIL','BOOTSTRAP_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')

function Read-LocalEnvSnapshot([string]$Path, [switch]$ProcessOnly) {
    $values = @{}
    if ($ProcessOnly) {
        foreach ($name in $script:LocalEnvKeys) { $values[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
        return [PSCustomObject]@{ Path=$Path; Exists=$false; Bytes=[byte[]]@(); Text=''; Bom=$false; Newline="`n"; Values=$values; ProcessOnly=$true }
    }
    $exists = Test-Path -LiteralPath $Path -PathType Leaf
    $bytes = [byte[]]@()
    $text = ''
    $bom = $false
    if ($exists) {
        $item = Get-Item -LiteralPath $Path -Force
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The .env file must not be a symbolic link or reparse point.' }
        $bytes = [IO.File]::ReadAllBytes($Path)
        if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) { $bom = $true }
        try { $text = [Text.UTF8Encoding]::new($false, $true).GetString($bytes, $(if ($bom) { 3 } else { 0 }), $bytes.Length - $(if ($bom) { 3 } else { 0 })) }
        catch { throw '.env must be a valid UTF-8 text file.' }
    } elseif (Test-Path -LiteralPath $Path) { throw 'The .env path must be a regular file.' }
    foreach ($line in ($text -split "`r?`n")) {
        if ($line -match '^\s*(#|$)') { continue }
        $parts = $line -split '=', 2
        if ($parts.Length -ne 2) { continue }
        $name = $parts[0].Trim()
        if ($script:LocalEnvKeys -notcontains $name) { continue }
        if ($values.ContainsKey($name)) { throw "$name appears more than once in .env. Remove the duplicate before starting." }
        $value = $parts[1]
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        if ($name -notmatch 'PASSWORD$' -and $name -notin @('ADMIN_API_KEY','READ_API_KEY')) { $value = $value.Trim() }
        $values[$name] = $value
    }
    $newline = if ($text.Contains("`r`n")) { "`r`n" } else { "`n" }
    return [PSCustomObject]@{ Path=$Path; Exists=$exists; Bytes=$bytes; Text=$text; Bom=$bom; Newline=$newline; Values=$values; ProcessOnly=$false }
}

function Assert-LocalEnvUnchanged($Snapshot) {
    if ($Snapshot.ProcessOnly) { return }
    if ((Test-Path -LiteralPath $Snapshot.Path -PathType Leaf) -ne $Snapshot.Exists) { throw '.env changed during initialization; retry after saving your edits.' }
    if ($Snapshot.Exists) {
        $item = Get-Item -LiteralPath $Snapshot.Path -Force
        if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'The .env file became unsafe; retry after restoring it.' }
        $bytes = [IO.File]::ReadAllBytes($Snapshot.Path)
        if ([Convert]::ToBase64String($bytes) -cne [Convert]::ToBase64String($Snapshot.Bytes)) { throw '.env changed during initialization; retry after saving your edits.' }
    }
}

function Assert-LocalEnvPrivate([string]$Path) {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $sections = [Security.AccessControl.AccessControlSections]::Owner -bor [Security.AccessControl.AccessControlSections]::Access
    $acl = [IO.File]::GetAccessControl($Path, $sections)
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    $allows = @($rules | Where-Object { $_.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow })
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid.Value -or -not $acl.AreAccessRulesProtected -or
        @($allows | Where-Object { $_.IdentityReference.Value -ne $sid.Value }).Count -gt 0 -or
        @($rules | Where-Object { $_.AccessControlType -eq [Security.AccessControl.AccessControlType]::Deny }).Count -gt 0 -or
        @($allows | Where-Object { $_.IdentityReference.Value -eq $sid.Value -and ($_.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -eq [Security.AccessControl.FileSystemRights]::FullControl }).Count -eq 0) {
        throw 'Could not verify private .env ownership and permissions. Check the file permissions before retrying.'
    }
}

function Protect-LocalEnv($Snapshot) {
    if (-not $Snapshot.Exists -or $Snapshot.ProcessOnly) { return }
    $item = Get-Item -LiteralPath $Snapshot.Path -Force
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'The .env file became unsafe; no credentials were written.' }
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    try {
        # Setting the owner on an existing file unnecessarily requires
        # WRITE_OWNER. Preserve it and modify only the access-control list.
        $ownerAcl = [IO.File]::GetAccessControl($Snapshot.Path, [Security.AccessControl.AccessControlSections]::Owner)
        if ($ownerAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $sid.Value) {
            throw 'The .env file must be owned by the current Windows user; ask its owner to adjust the file permissions before retrying.'
        }
        $acl = [IO.File]::GetAccessControl($Snapshot.Path, [Security.AccessControl.AccessControlSections]::Access)
        $acl.SetAccessRuleProtection($true, $false)
        foreach ($rule in @($acl.GetAccessRules($true, $false, [Security.Principal.SecurityIdentifier]))) {
            $acl.RemoveAccessRuleSpecific($rule)
        }
        $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.AccessControlType]::Allow))
        [IO.File]::SetAccessControl($Snapshot.Path, $acl)
        Assert-LocalEnvPrivate $Snapshot.Path
    } catch {
        $cause = $_.Exception
        while ($cause.InnerException) { $cause = $cause.InnerException }
        if ($cause -is [UnauthorizedAccessException] -or $cause -is [Security.SecurityException]) {
            throw 'Cannot secure .env with the current Windows account. The account must own the file and be allowed to change its access permissions; ask the file owner for help. No database was deleted.'
        }
        throw
    }
}

function Save-LocalEnvSnapshot($Snapshot, [hashtable]$Updates) {
    if ($Snapshot.ProcessOnly) { throw '-NoEnvFile cannot generate or migrate persistent configuration. Use the project .env.' }
    $remaining = @{}
    foreach ($name in $Updates.Keys) {
        if ($script:LocalEnvKeys -notcontains $name) { throw 'An unsupported .env field was requested.' }
        $value = [string]$Updates[$name]
        if ($value.IndexOfAny([char[]]@([char]0,[char]10,[char]13)) -ge 0) { throw 'A local configuration value contains an invalid line break or NUL.' }
        if (-not $Snapshot.Values.ContainsKey($name) -or [string]$Snapshot.Values[$name] -cne $value) { $remaining[$name] = $value }
    }
    if ($remaining.Count -eq 0) { return $Snapshot }
    $builder = [Text.StringBuilder]::new()
    foreach ($line in [regex]::Split($Snapshot.Text, '(?<=\n)')) {
        if ($line -match '^(\s*([A-Za-z_][A-Za-z0-9_]*)\s*=)[^\r\n]*(\r?\n)?$' -and $remaining.ContainsKey($Matches[2])) {
            $name = $Matches[2]; $prefix = $Matches[1]; $ending = $Matches[3]
            $null = $builder.Append($prefix).Append('"').Append($remaining[$name]).Append('"').Append($ending)
            $remaining.Remove($name)
        } else { $null = $builder.Append($line) }
    }
    foreach ($name in @($remaining.Keys | Sort-Object)) {
        if ($builder.Length -gt 0 -and $builder[$builder.Length - 1] -ne "`n") { $null = $builder.Append($Snapshot.Newline) }
        $null = $builder.Append($name).Append('="').Append($remaining[$name]).Append('"').Append($Snapshot.Newline)
    }
    $textBytes = [Text.UTF8Encoding]::new($false, $true).GetBytes($builder.ToString())
    if ($Snapshot.Bom) {
        $writeBytes = [byte[]]::new($textBytes.Length + 3)
        $writeBytes[0]=0xEF; $writeBytes[1]=0xBB; $writeBytes[2]=0xBF
        [Array]::Copy($textBytes, 0, $writeBytes, 3, $textBytes.Length)
    } else { $writeBytes = $textBytes }
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = [Security.AccessControl.FileSecurity]::new()
    $acl.SetOwner($sid)
    $acl.SetAccessRuleProtection($true, $false)
    $null = $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.AccessControlType]::Allow))
    $temp = Join-Path ([IO.Path]::GetDirectoryName($Snapshot.Path)) ('.env-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $stream = $null; $original = $null
    try {
        if ($Snapshot.Exists) {
            # Protect the existing file without asking Windows to set its owner.
            Protect-LocalEnv $Snapshot
        }
        $stream = [IO.FileStream]::new($temp, [IO.FileMode]::CreateNew, [Security.AccessControl.FileSystemRights]::Write, [IO.FileShare]::None, 4096, [IO.FileOptions]::None, $acl)
        $stream.Write($writeBytes, 0, $writeBytes.Length); $stream.Flush($true); $stream.Dispose(); $stream = $null
        if ($Snapshot.Exists) {
            $original = [IO.FileStream]::new($Snapshot.Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, ([IO.FileShare]::Read -bor [IO.FileShare]::Delete))
            $actual = [byte[]]::new([int]$original.Length)
            $offset = 0
            while ($offset -lt $actual.Length) { $n=$original.Read($actual,$offset,$actual.Length-$offset); if ($n -eq 0) { throw '.env changed while being read.' }; $offset += $n }
            if ([Convert]::ToBase64String($actual) -cne [Convert]::ToBase64String($Snapshot.Bytes)) { throw '.env changed during initialization; no values were overwritten. Retry after saving your edits.' }
            # Never ignore ACL/metadata merge errors during the atomic replace.
            [IO.File]::Replace($temp, $Snapshot.Path, [System.Management.Automation.Language.NullString]::Value, $false)
        } else {
            [IO.File]::Move($temp, $Snapshot.Path)
        }
        Assert-LocalEnvPrivate $Snapshot.Path
    } finally {
        if ($original) { $original.Dispose() }
        if ($stream) { $stream.Dispose() }
        if (Test-Path -LiteralPath $temp -PathType Leaf) { Remove-Item -LiteralPath $temp -Force -ErrorAction SilentlyContinue }
        [Array]::Clear($writeBytes, 0, $writeBytes.Length)
    }
    return Read-LocalEnvSnapshot $Snapshot.Path
}
