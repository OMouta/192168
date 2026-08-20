<#
.SYNOPSIS
    Downloads the Wintun driver the daemon needs.

.DESCRIPTION
    Wintun is the virtual network adapter, written by the WireGuard authors. It
    is not redistributed in this repository, so it is fetched and placed next to
    the daemon.

    The extracted DLL has to carry a valid Authenticode signature from WireGuard
    LLC or the script refuses it. That is the check that matters for a driver:
    it says who published the file, not merely that the bytes arrived intact.

    The archive hash is pinned by default, so a download that produces different
    bytes stops the script instead of installing them.

.PARAMETER Destination
    Where to put wintun.dll. Defaults to the dev bin directory.

.PARAMETER Version
    Wintun release to fetch. Fetching a different version needs a matching
    -ExpectedHash, or the pinned one will reject it.

.PARAMETER ExpectedHash
    SHA256 of the archive, pinned by default. Pass an empty string to skip the
    check, which prints the hash it saw so you can compare it against
    wintun.net and pin that instead.

.EXAMPLE
    .\scripts\fetch-wintun.ps1
    .\scripts\fetch-wintun.ps1 -Version 0.14.1 -ExpectedHash ABC123...
#>

[CmdletBinding()]
param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA '192168-dev\bin'),
    [string]$Version = '0.14.1',
    # Recorded from a download whose signature verified as WireGuard LLC. It is
    # here so a later run notices if the bytes change. Cross-check it against
    # wintun.net if you would rather not take this file's word for it.
    [string]$ExpectedHash = '07C256185D6EE3652E09FA55C0B673E2624B565E02C4B9091C79CA7D2F24EF51'
)

$ErrorActionPreference = 'Stop'

$expectedSigner = 'WireGuard LLC'

$architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    'x86' { 'x86' }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

function Test-WintunSignature {
    param([string]$Path)

    $signature = Get-AuthenticodeSignature $Path
    if ($signature.Status -ne 'Valid') {
        return "not validly signed: $($signature.Status)"
    }
    if ($signature.SignerCertificate.Subject -notlike "*$expectedSigner*") {
        return "signed by someone else: $($signature.SignerCertificate.Subject)"
    }
    return $null
}

$target = Join-Path $Destination 'wintun.dll'
# The installer ships this beside the DLL.
$licenseTarget = Join-Path $Destination 'wintun-LICENSE.txt'

if ((Test-Path $target) -and (Test-Path $licenseTarget)) {
    $problem = Test-WintunSignature $target
    if (-not $problem) {
        Write-Host "wintun.dll is already in place, signed by $expectedSigner." -ForegroundColor Green
        exit 0
    }
    Write-Host "Replacing the wintun.dll that is there: $problem" -ForegroundColor Yellow
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) "wintun-$([guid]::NewGuid())"
New-Item -ItemType Directory -Force -Path $work | Out-Null

try {
    $archive = Join-Path $work "wintun-$Version.zip"
    $url = "https://www.wintun.net/builds/wintun-$Version.zip"

    Write-Host "Downloading $url" -ForegroundColor Cyan
    Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing

    $hash = (Get-FileHash $archive -Algorithm SHA256).Hash
    if ($ExpectedHash) {
        if ($hash -ne $ExpectedHash.Trim().ToUpperInvariant()) {
            throw "The archive hash does not match.`n  expected $ExpectedHash`n  got      $hash"
        }
        Write-Host 'Archive hash matches the pinned value.' -ForegroundColor Green
    }
    else {
        Write-Host "Archive SHA256: $hash" -ForegroundColor DarkGray
        Write-Host 'Compare that against wintun.net and pass it as -ExpectedHash to pin it.' -ForegroundColor DarkGray
    }

    Expand-Archive -Path $archive -DestinationPath $work -Force
    $dll = Join-Path $work "wintun\bin\$architecture\wintun.dll"
    if (-not (Test-Path $dll)) {
        throw "The archive has no build for $architecture."
    }

    $problem = Test-WintunSignature $dll
    if ($problem) {
        throw "Refusing to install wintun.dll: it is $problem"
    }
    Write-Host "Signed by $expectedSigner." -ForegroundColor Green

    $license = Join-Path $work 'wintun\LICENSE.txt'
    if (-not (Test-Path $license)) {
        throw 'The archive has no LICENSE.txt.'
    }

    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Copy-Item $dll $target -Force
    Copy-Item $license $licenseTarget -Force
    Write-Host "Placed $target" -ForegroundColor Green
    Write-Host "Placed $licenseTarget" -ForegroundColor Green
}
finally {
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}
