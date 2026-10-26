# Runs the Go black-box e2e suite (e2e/) against the C# server.
#
# Usage (from anywhere):  powershell -File dotnet\e2e.ps1 [-Port 18090] [-NoBuild]
#
# Needs e2e/.env.e2e (the same file the Go suite uses; see e2e/.env.e2e.example) pointing at a
# dedicated e2e schema: the suite DELETEs every row of master_machine, temp_log,
# temp_error, temp_log_archive and archive_manifest. The C# server is started
# from a temp directory holding a copy of .env.e2e with every MQTT_* and
# LEGACY_API_* line removed, so nothing is published or sent anywhere.
param(
    [int]$Port = 18090,
    [switch]$NoBuild
)
$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$envE2e = Join-Path $repo 'e2e\.env.e2e'
if (-not (Test-Path $envE2e)) { throw ".env.e2e not found at $envE2e" }

$dotnet = (Get-Command dotnet -ErrorAction SilentlyContinue).Source
if (-not $dotnet) { $dotnet = Join-Path $env:ProgramFiles 'dotnet\dotnet.exe' }
$project = Join-Path $PSScriptRoot 'src\TmsBackend\TmsBackend.csproj'
if (-not $NoBuild) {
    & $dotnet build $project -c Debug -v q -nologo
    if ($LASTEXITCODE -ne 0) { throw 'build failed' }
}
$exe = Get-ChildItem (Join-Path $PSScriptRoot 'src\TmsBackend\bin\Debug') -Recurse -Filter 'tms-backend.exe' |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $exe) { throw 'tms-backend.exe not found; build first' }
if (Test-Path (Join-Path $exe.DirectoryName '.env')) { throw "$($exe.DirectoryName)\.env would override .env.e2e; remove it" }

$work = Join-Path ([IO.Path]::GetTempPath()) "tms-cs-e2e-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
$archiveDir = Join-Path $work 'archives'
Get-Content $envE2e | Where-Object { $_ -notmatch '^\s*(MQTT_|LEGACY_API_)' } |
    Set-Content -Encoding utf8 (Join-Path $work '.env')

# Real environment variables win over .env (no clobber), on both sides.
$env:PORT = "$Port"
$env:POLLING_AUTOSTART = 'false'
$env:ARCHIVE_DIR = $archiveDir
$env:ARCHIVE_RETENTION_DAYS = '30'
$env:E2E_BASE_URL = "http://127.0.0.1:$Port"
$env:E2E_ARCHIVE_DIR = $archiveDir

$server = Start-Process -FilePath $exe.FullName -WorkingDirectory $work -PassThru -NoNewWindow `
    -RedirectStandardOutput (Join-Path $work 'server.out.log') -RedirectStandardError (Join-Path $work 'server.err.log')
try {
    $deadline = (Get-Date).AddSeconds(60)
    while ($true) {
        try {
            $health = Invoke-RestMethod "http://127.0.0.1:$Port/health" -TimeoutSec 2
            if ($health.status -eq 'ok') { break }
        } catch { }
        if ($server.HasExited) { throw "server exited early (code $($server.ExitCode)); see $work\server.out.log" }
        if ((Get-Date) -gt $deadline) { throw "server not healthy after 60s; see $work\server.out.log" }
        Start-Sleep -Milliseconds 500
    }

    Push-Location $repo
    try {
        go test -tags=e2e -count=1 -v ./e2e/...
        $code = $LASTEXITCODE
    } finally {
        Pop-Location
    }
} finally {
    if (-not $server.HasExited) { Stop-Process -Id $server.Id -Force }
}
Write-Host "server log: $work\server.out.log"
exit $code
