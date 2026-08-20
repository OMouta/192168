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

# Windows version resources are four numbers and nothing else, so a tag like
# 0.2.0-rc1 has to be reduced to one before it can be stamped into a binary.
$number = [version](($Version -split '-')[0])
$numericVersion = '{0}.{1}.{2}.0' -f $number.Major, [math]::Max($number.Minor, 0), [math]::Max($number.Build, 0)

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
        # Go emits no version resource, so without this the UAC prompt names the
        # file instead of the app. The version is passed rather than read from
        # versioninfo.json, which carries a development default that a release
        # would otherwise ship as its real version.
        Push-Location (Join-Path $repo 'daemon\cmd\192168-service')
        try {
            & go tool goversioninfo -o resource.syso `
                -ver-major $number.Major `
                -ver-minor ([math]::Max($number.Minor, 0)) `
                -ver-patch ([math]::Max($number.Build, 0)) `
                -product-version $Version `
                versioninfo.json
            if ($LASTEXITCODE -ne 0) { throw 'goversioninfo failed, so the binary would have no version resource' }
        }
        finally {
            Pop-Location
        }

        $env:GOOS = 'windows'
        $env:GOARCH = 'amd64'
        & go build -trimpath -ldflags '-s -w' -o (Join-Path $stage '192168-service.exe') ./daemon/cmd/192168-service
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    }
    finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
        Pop-Location
    }

    Write-Host 'Building the client...' -ForegroundColor Cyan
    # Build, not publish. The csproj is already self-contained, and publish drops
    # Net192168.Client.pri, which holds the compiled XAML. Without it every
    # window fails to parse and the app dies before it draws anything.
    & dotnet build $csproj -c Release -r win-x64 -p:Platform=x64 `
        -p:Version=$Version --nologo -v quiet -o (Join-Path $stage 'client')
    if ($LASTEXITCODE -ne 0) { throw 'dotnet build failed' }
}
else {
    Write-Host 'Skipping the build, staging what is already there.' -ForegroundColor Yellow
    Copy-Item (Join-Path $repo 'client\windows\Net192168.Client\bin\x64\Release\net10.0-windows10.0.26100.0\win-x64\*') `
        (Join-Path $stage 'client') -Recurse -Force -ErrorAction SilentlyContinue
    Copy-Item (Join-Path $env:LOCALAPPDATA '192168-dev\bin\192168-service.exe') $stage -Force
}

# Wintun, signature-checked by the fetch script.
Write-Host 'Fetching Wintun...' -ForegroundColor Cyan
& (Join-Path $PSScriptRoot 'fetch-wintun.ps1') -Destination $stage

Copy-Item (Join-Path $repo 'THIRD-PARTY-NOTICES.md') $stage -Force
Copy-Item (Join-Path $repo 'LICENSE') (Join-Path $stage 'LICENSE.txt') -Force

foreach ($required in @('192168-service.exe', 'wintun.dll', 'wintun-LICENSE.txt', 'LICENSE.txt')) {
    if (-not (Test-Path (Join-Path $stage $required))) { throw "Missing from the stage directory: $required" }
}
# The pri holds the compiled XAML. Shipping without it produces an installer
# that lays everything down and an app that dies parsing its first window, which
# is only visible on the machine that installed it.
foreach ($required in @('Net192168.Client.exe', 'Net192168.Client.pri', 'Assets\icon.ico', 'Assets\wordmark.png')) {
    if (-not (Test-Path (Join-Path $stage "client\$required"))) {
        throw "Missing from the stage directory: client\$required"
    }
}

Write-Host 'Compiling the installer...' -ForegroundColor Cyan
& $iscc `
    "/DAppVersion=$Version" `
    "/DAppVersionNumeric=$numericVersion" `
    "/DStageDir=$stage" `
    (Join-Path $repo 'installer\192168.iss')
if ($LASTEXITCODE -ne 0) { throw 'ISCC failed' }

$setup = Join-Path $build "192168-$Version-setup.exe"
$size = [math]::Round((Get-Item $setup).Length / 1MB, 1)

Write-Host ''
Write-Host "Built $setup ($size MB)" -ForegroundColor Green
Write-Host 'Not signed, so SmartScreen will warn about it.' -ForegroundColor DarkYellow
