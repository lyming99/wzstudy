$ErrorActionPreference = 'Stop'
$target = Join-Path (Split-Path $PSScriptRoot -Parent) '.env'
if (Test-Path $target) { Write-Host '.env already exists; left unchanged.'; return }
function New-Secret {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}
$text = @"
# Local credentials. Do not commit or share this file.
APP_ENV=production
POSTGRES_PASSWORD=$(New-Secret)
ADMIN_API_KEY=$(New-Secret)
READ_API_KEY=$(New-Secret)
API_PORT=8091
POLL_ENABLED=true
CORS_ORIGINS=
"@
# CreateNew prevents a concurrent run from overwriting existing credentials.
$stream = [IO.File]::Open($target, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try {
    $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes($text)
    $stream.Write($bytes, 0, $bytes.Length)
} finally { $stream.Dispose() }
Write-Host 'Created project .env with independent random credentials (not printed).'
