# Pure definitions; no credential/configuration files are read until called.
function Read-AdministratorEnvironment {
    [CmdletBinding()]
    param([Parameter(Mandatory=$true)][string]$ProjectRoot, [switch]$NoEnvFile)
    $Groups = [ordered]@{
        SYSTEM_ADMIN_EMAIL = @('SYSTEM_ADMIN_EMAIL','BOOTSTRAP_ADMIN_EMAIL')
        SYSTEM_ADMIN_PASSWORD = @('SYSTEM_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_PASSWORD')
        SYSTEM_ADMIN_DISPLAY_NAME = @('SYSTEM_ADMIN_DISPLAY_NAME','SYSTEM_ADMIN_USERNAME','BOOTSTRAP_ADMIN_DISPLAY_NAME','BOOTSTRAP_ADMIN_USERNAME')
    }
    $Resolved = @{}
    foreach ($Canonical in $Groups.Keys) {
        $Resolved[$Canonical] = $null
        foreach ($Name in $Groups[$Canonical]) {
            $Value = [Environment]::GetEnvironmentVariable($Name,'Process')
            if ($Canonical -ne 'SYSTEM_ADMIN_PASSWORD' -and $null -ne $Value) { $Value = $Value.Trim() }
            if ($null -ne $Value -and $Value -ne '') { $Resolved[$Canonical] = $Value; break }
        }
    }
    $EnvPath = Join-Path $ProjectRoot '.env'
    if (-not $NoEnvFile -and (Test-Path -LiteralPath $EnvPath) -and
        (-not $Resolved['SYSTEM_ADMIN_EMAIL'] -or -not $Resolved['SYSTEM_ADMIN_PASSWORD'] -or -not $Resolved['SYSTEM_ADMIN_DISPLAY_NAME'])) {
        $FileValues = @{}
        $Allowed = @(); foreach ($Names in $Groups.Values) { $Allowed += $Names }
        foreach ($Line in Get-Content -LiteralPath $EnvPath -Encoding UTF8) {
            if ($Line -match '^\s*(#|$)') { continue }
            $Parts = $Line -split '=',2
            if ($Parts.Length -ne 2) { continue }
            $Name = $Parts[0].Trim()
            if ($Allowed -notcontains $Name -or $FileValues.ContainsKey($Name)) { continue }
            $Value = $Parts[1]
            if ($Value.Length -ge 2 -and (($Value.StartsWith('"') -and $Value.EndsWith('"')) -or ($Value.StartsWith("'") -and $Value.EndsWith("'")))) {
                $Value = $Value.Substring(1,$Value.Length-2)
            }
            if ($Name -notmatch 'PASSWORD$') { $Value = $Value.Trim() }
            $FileValues[$Name] = $Value
        }
        foreach ($Canonical in $Groups.Keys) {
            if ($Resolved[$Canonical]) { continue } # Process aliases win as one logical field.
            foreach ($Name in $Groups[$Canonical]) {
                if ($FileValues.ContainsKey($Name) -and $FileValues[$Name] -ne '') { $Resolved[$Canonical] = $FileValues[$Name]; break }
            }
        }
    }
    return $Resolved
}
