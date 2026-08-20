<#
.SYNOPSIS
    Builds the installer.

.DESCRIPTION
    Compiles the daemon and the client, fetches the Wintun driver, stages
    everything in one directory, and hands that to Inno Setup.

    The result is build\192168-<version>-setup.exe.

    Nothing is signed, so SmartScreen will warn about the result.

.PARAMETER Version
    Version stamped into the installer. Defaults to the client's own, read from
    the csproj so the two cannot disagree.

.PARAMETER SkipBuild
    Stage and compile the installer from whatever was built last. For iterating
    on the .iss file.

.EXAMPLE
    .\scripts\package.ps1
    .\scripts\package.ps1 -Version 0.2.0
#>

[CmdletBinding()]
param(
    [string]$Version,
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$build = Join-Path $repo 'build'
$stage = Join-Path $build 'stage'
$client = Join-Path $repo 'client\windows\Net192168.Client'
$csproj = Join-Path $client 'Net192168.Client.csproj'

# The version lives in the csproj, which is also what the About screen reads.
if (-not $Version) {
    $found = Select-String -Path $csproj -Pattern '<Version>([^<]+)</Version>'
    if (-not $found) { throw "No <Version> in $csproj, and none passed with -Version." }
    $Version = $found.Matches[0].Groups[1].Value
}

Write-Host "Packaging 192168 $Version" -ForegroundColor Cyan

# Inno Setup is not fetched automatically.
$iscc = (Get-Command ISCC.exe -ErrorAction SilentlyContinue)?.Source
if (-not $iscc) {
    foreach ($candidate in @(
            "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
            "$env:ProgramFiles\Inno Setup 6\ISCC.exe",
            # Where winget puts it, since it installs per user by default.
            "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe")) {
        if (Test-Path $candidate) { $iscc = $candidate; break }
    }
}
if (-not $iscc) {
    throw "Inno Setup 6 is not installed. Get it from https://jrsoftware.org/isdl.php, or: winget install JRSoftware.InnoSetup"
}

# A stale file here would be shipped.
Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $stage, (Join-Path $stage 'client') | Out-Null

if (-not $SkipBuild) {
    Write-Host 'Building the daemon...' -ForegroundColor Cyan
    Push-Location $repo
    try {
        $env:GOOS = 'windows'
        $env:GOARCH = 'amd64'
        & go build -trimpath -ldflags '-s -w' -o (Join-Path $stage '192168-daemon.exe') ./daemon/cmd/192168-daemon
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    }
    finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
        Pop-Location
    }

    Write-Host 'Publishing the client...' -ForegroundColor Cyan
    # Publish, not build: the client is self-contained, so this is what runs on a
    # machine with no .NET and no Windows App SDK.
    & dotnet publish $csproj -c Release -r win-x64 -p:Platform=x64 `
        -p:Version=$Version --nologo -v quiet -o (Join-Path $stage 'client')
    if ($LASTEXITCODE -ne 0) { throw 'dotnet publish failed' }
}
else {
    Write-Host 'Skipping the build, staging what is already there.' -ForegroundColor Yellow
    Copy-Item (Join-Path $repo 'client\windows\Net192168.Client\bin\Release\net10.0-windows10.0.26100.0\win-x64\publish\*') `
        (Join-Path $stage 'client') -Recurse -Force -ErrorAction SilentlyContinue
    Copy-Item (Join-Path $env:LOCALAPPDATA '192168-dev\bin\192168-daemon.exe') $stage -Force
}

# Wintun, signature-checked by the fetch script.
Write-Host 'Fetching Wintun...' -ForegroundColor Cyan
& (Join-Path $PSScriptRoot 'fetch-wintun.ps1') -Destination $stage

Copy-Item (Join-Path $repo 'THIRD-PARTY-NOTICES.md') $stage -Force
Copy-Item (Join-Path $repo 'LICENSE') (Join-Path $stage 'LICENSE.txt') -Force

foreach ($required in @('192168-daemon.exe', 'wintun.dll', 'wintun-LICENSE.txt', 'LICENSE.txt')) {
    if (-not (Test-Path (Join-Path $stage $required))) { throw "Missing from the stage directory: $required" }
}
if (-not (Test-Path (Join-Path $stage 'client\Net192168.Client.exe'))) {
    throw 'Missing from the stage directory: client\Net192168.Client.exe'
}

Write-Host 'Compiling the installer...' -ForegroundColor Cyan
& $iscc `
    "/DAppVersion=$Version" `
    "/DStageDir=$stage" `
    (Join-Path $repo 'installer\192168.iss')
if ($LASTEXITCODE -ne 0) { throw 'ISCC failed' }

$setup = Join-Path $build "192168-$Version-setup.exe"
$size = [math]::Round((Get-Item $setup).Length / 1MB, 1)

Write-Host ''
Write-Host "Built $setup ($size MB)" -ForegroundColor Green
Write-Host 'Not signed, so SmartScreen will warn about it.' -ForegroundColor DarkYellow
