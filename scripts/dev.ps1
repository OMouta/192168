<#
.SYNOPSIS
    Builds and runs the whole stack for development.

.DESCRIPTION
    Starts a local coordination server, points the daemon at it, and opens the
    client. The three pieces normally live on different machines, so running
    them together is the only way to click through anything.

    The server and the daemon each get their own console with a header saying
    which is which, and their logs go to the window as well as to a file.

    Server data and binaries go under %LOCALAPPDATA%\192168-dev. The daemon
    keeps its identity in its real location, %APPDATA%\192168, so a device stays
    the same device between runs.

.PARAMETER Port
    Port for the local server. Defaults to 8080.

.PARAMETER NoClient
    Start the server and daemon but not the window.

.PARAMETER Reset
    Delete the local server database and the device identity first, which is the
    difference between testing a returning user and a new one.

.PARAMETER Stop
    Stop everything this script started and exit.

.EXAMPLE
    .\scripts\dev.ps1
    .\scripts\dev.ps1 -Reset
    .\scripts\dev.ps1 -Stop
#>

[CmdletBinding()]
param(
    [int]$Port = 8080,
    [switch]$NoClient,
    [switch]$Reset,
    [switch]$Stop
)

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$devRoot = Join-Path $env:LOCALAPPDATA '192168-dev'
$binDir = Join-Path $devRoot 'bin'
$logDir = Join-Path $devRoot 'logs'
$pidFile = Join-Path $devRoot 'consoles.txt'
$identityDir = Join-Path $env:APPDATA '192168'
$clientExe = Join-Path $repo 'client\windows\Net192168.Client\bin\Debug\net10.0-windows10.0.26100.0\win-x64\Net192168.Client.exe'

# pwsh where it exists, since Windows PowerShell renders the log colours worse.
$shell = if (Get-Command pwsh -ErrorAction SilentlyContinue) { 'pwsh' } else { 'powershell' }

function Stop-Stack {
    foreach ($name in '192168-server', '192168-daemon', 'Net192168.Client') {
        Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force
    }
    # The consoles outlive the process they were hosting, so they are tracked
    # by id and closed here rather than left as dead prompts.
    if (Test-Path $pidFile) {
        foreach ($id in Get-Content $pidFile) {
            Get-Process -Id $id -ErrorAction SilentlyContinue | Stop-Process -Force
        }
        Remove-Item $pidFile -Force -ErrorAction SilentlyContinue
    }
}

if ($Stop) {
    Stop-Stack
    Write-Host 'Stopped.' -ForegroundColor Yellow
    exit 0
}

# Rebuilding over a running binary fails with a file lock, so nothing is built
# until the old processes are gone.
Stop-Stack
Start-Sleep -Milliseconds 500

if ($Reset) {
    Remove-Item (Join-Path $devRoot 'server.db*') -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $identityDir 'identity.json') -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $identityDir 'settings.json') -Force -ErrorAction SilentlyContinue
    Write-Host 'Cleared the server database and this device identity.' -ForegroundColor Yellow
}

New-Item -ItemType Directory -Force -Path $binDir, $logDir | Out-Null

Write-Host 'Building Go...' -ForegroundColor Cyan
Push-Location $repo
try {
    & go build -o "$binDir\" ./server/cmd/192168-server ./daemon/cmd/192168-daemon
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
}
finally {
    Pop-Location
}

if (-not $NoClient) {
    Write-Host 'Building the client...' -ForegroundColor Cyan
    & dotnet build (Join-Path $repo 'client\windows\192168.slnx') -p:Platform=x64 --nologo -v quiet
    if ($LASTEXITCODE -ne 0) { throw 'dotnet build failed' }
}

$serverUrl = "http://localhost:$Port"

<#
.SYNOPSIS
    Opens a console that announces what it is and then runs one process.
#>
function Start-Console {
    param(
        [string]$Title,
        [string]$Colour,
        [string]$Exe,
        [string]$LogFile,
        [hashtable]$Environment,
        [string[]]$Notes
    )

    $lines = @(
        "`$Host.UI.RawUI.WindowTitle = '$Title'"
        "Write-Host ''"
        "Write-Host '  $Title  ' -ForegroundColor Black -BackgroundColor $Colour"
    )
    foreach ($note in $Notes) {
        $lines += "Write-Host '  $note' -ForegroundColor DarkGray"
    }
    $lines += "Write-Host ''"
    foreach ($pair in $Environment.GetEnumerator()) {
        $lines += "`$env:$($pair.Key) = '$($pair.Value)'"
    }
    # Tee so the window shows the log live and the file still has it afterwards.
    $lines += "& '$Exe' 2>&1 | Tee-Object -FilePath '$LogFile'"
    $lines += "Write-Host ''"
    $lines += "Write-Host '  $Title exited.' -ForegroundColor Red"

    $console = Start-Process $shell -PassThru -ArgumentList @(
        '-NoLogo', '-NoExit', '-Command', ($lines -join '; ')
    )
    Add-Content -Path $pidFile -Value $console.Id
    return $console
}

Write-Host "Starting the server on $serverUrl" -ForegroundColor Cyan
Start-Console -Title '192168 SERVER' -Colour 'Green' `
    -Exe (Join-Path $binDir '192168-server.exe') `
    -LogFile (Join-Path $logDir 'server.log') `
    -Notes @("coordination server  $serverUrl", 'introduces peers, carries no game traffic') `
    -Environment @{
        NET192168_PUBLIC_URL   = $serverUrl
        NET192168_ADDR         = ":$Port"
        NET192168_DATABASE_URL = Join-Path $devRoot 'server.db'
    } | Out-Null

# The daemon registers with the server on its first call, so the server has to
# be answering before it is worth starting.
$ready = $false
foreach ($attempt in 1..30) {
    Start-Sleep -Milliseconds 200
    try {
        Invoke-RestMethod "$serverUrl/api/health" -TimeoutSec 2 | Out-Null
        $ready = $true
        break
    }
    catch { }
}
if (-not $ready) { throw "The server never answered on $serverUrl. Its console will say why." }

Write-Host 'Starting the daemon' -ForegroundColor Cyan
Start-Console -Title '192168 DAEMON' -Colour 'Cyan' `
    -Exe (Join-Path $binDir '192168-daemon.exe') `
    -LogFile (Join-Path $logDir 'daemon.log') `
    -Notes @('networking daemon    pipe \\.\pipe\192168', "talking to  $serverUrl") `
    -Environment @{ NET192168_SERVER_URL = $serverUrl } | Out-Null

Start-Sleep -Seconds 1
if (-not (Get-Process -Name '192168-daemon' -ErrorAction SilentlyContinue)) {
    throw 'The daemon exited. Its console will say why.'
}

if (-not $NoClient) {
    if (-not (Test-Path $clientExe)) { throw "The client was not built: $clientExe" }
    Write-Host 'Opening the client' -ForegroundColor Cyan
    Start-Process $clientExe | Out-Null
}

Write-Host ''
Write-Host 'Running.' -ForegroundColor Green
Write-Host "  server    $serverUrl"
Write-Host "  logs      $logDir"
Write-Host "  crashes   $(Join-Path $identityDir 'client-crash.log')"
Write-Host ''
Write-Host 'Stop everything with: .\scripts\dev.ps1 -Stop' -ForegroundColor DarkGray
