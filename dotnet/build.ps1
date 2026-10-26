# Builds tms-backend.exe (single-file, self-contained, win-x64) and the
# Setup.exe installer — the C# replacement for build.sh.
#
# Usage:  powershell -ExecutionPolicy Bypass -File dotnet\build.ps1 [-Version 1.2.3] [-SkipInstaller] [-SkipTests]
#
# Version precedence (same as build.sh): -Version / env VERSION > VERSION in the
# repo .env > git describe > "dev".
# Output: dotnet\publish\tms-backend.exe and dotnet\dist\tms-backend-setup-<version>.exe
param(
    [string]$Version = $env:VERSION,
    [switch]$SkipInstaller,
    [switch]$SkipTests
)
$ErrorActionPreference = 'Stop'

$root = $PSScriptRoot
$repo = Split-Path -Parent $root

if (-not $Version) {
    $envFile = Join-Path $repo '.env'
    if (Test-Path $envFile) {
        $line = Get-Content $envFile | Where-Object { $_ -match '^VERSION=' } | Select-Object -Last 1
        if ($line) { $Version = ($line -replace '^VERSION=', '').Trim().Trim('"', "'") }
    }
}
if (-not $Version) {
    $Version = git -C $repo describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -ne 0) { $Version = $null }
}
if (-not $Version) { $Version = 'dev' }

# Assembly/file versions must be numeric: take the leading x.y.z of e.g. "v1.4.2-3-gabc".
$numeric = '0.0.0'
# Needs at least "x.y" so a bare commit hash like "8f714b7" is not read as 8.0.0.
if ($Version -match '^v?(\d+)\.(\d+)(?:\.(\d+))?') {
    $parts = 1..3 | ForEach-Object { if ($Matches[$_]) { $Matches[$_] } else { '0' } }
    $numeric = $parts -join '.'
}
Write-Host "Version: $Version (file version $numeric)"

$dotnet = (Get-Command dotnet -ErrorAction SilentlyContinue).Source
if (-not $dotnet) { $dotnet = Join-Path $env:ProgramFiles 'dotnet\dotnet.exe' }

if (-not $SkipTests) {
    & $dotnet test (Join-Path $root 'TmsBackend.sln') -v q -nologo
    if ($LASTEXITCODE -ne 0) { throw 'unit tests failed' }
}

$publish = Join-Path $root 'publish'
if (Test-Path $publish) { Remove-Item -Recurse -Force $publish }
& $dotnet publish (Join-Path $root 'src\TmsBackend\TmsBackend.csproj') -c Release -r win-x64 --self-contained `
    -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -p:EnableCompressionInSingleFile=true `
    -p:DebugType=none -p:Version=$numeric -p:InformationalVersion=$Version -o $publish -v q -nologo
if ($LASTEXITCODE -ne 0) { throw 'publish failed' }

$exe = Join-Path $publish 'tms-backend.exe'
$reported = & $exe --version
if ($reported -ne $Version) { throw "built exe reports version '$reported', expected '$Version'" }
Write-Host "Built $exe ($([math]::Round((Get-Item $exe).Length / 1MB, 1)) MB)"

if ($SkipInstaller) { return }

$iscc = @(
    (Get-Command ISCC.exe -ErrorAction SilentlyContinue).Source,
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $iscc) { throw 'ISCC.exe not found: install Inno Setup 6 (winget install JRSoftware.InnoSetup)' }

$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null
& $iscc /Q "/DAppVersion=$Version" "/DFileVersion=$numeric" "/DPublishDir=$publish" "/O$dist" `
    (Join-Path $root 'installer\tms-backend.iss')
if ($LASTEXITCODE -ne 0) { throw 'installer compile failed' }
Get-ChildItem $dist -Filter "tms-backend-setup-$Version.exe" | ForEach-Object { Write-Host "Built $($_.FullName)" }
