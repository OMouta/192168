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

.PARAMETER Hosted
    Skip the local server and talk to the deployed one instead, which is what
    two machines on different networks have to do. The address is not repeated
    here: the daemon falls back to its own DefaultServerURL.

    This also deletes settings.json, because a server picked in the Settings
    screen is stored there and beats the built-in default. That file holds
    nothing else.

    Combined with -Reset the daemon registers as a new device against the real
    server, so use it deliberately rather than out of habit.

.PARAMETER NoClient
    Start the server and daemon but not the window.

.PARAMETER Reset
    Delete the local server database and the device identity first, which is the
    difference between testing a returning user and a new one.

.PARAMETER Elevated
    Run the daemon as administrator, which is what creating the virtual adapter
    needs. Without this the daemon runs and links open, but no game traffic
    moves and the app says why. Expect a UAC prompt.

.PARAMETER LogLevel
    How much the server and daemon say. Debug includes a line per packet that
    reaches the adapter, which is how you tell a packet arriving from a packet
    being imagined.

.PARAMETER Stop
    Stop everything this script started and exit.

.EXAMPLE
    .\scripts\dev.ps1
    .\scripts\dev.ps1 -Reset
    .\scripts\dev.ps1 -Hosted -Elevated
    .\scripts\dev.ps1 -Stop
#>

[CmdletBinding()]
param(
    [int]$Port = 8080,
    [switch]$Hosted,
    [switch]$NoClient,
    [switch]$Reset,
    [switch]$Stop,
    [switch]$Elevated,
    [ValidateSet('debug', 'info', 'warn', 'error')]
    [string]$LogLevel = 'info'
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
    if (-not $Hosted) {
        Remove-Item (Join-Path $devRoot 'server.db*') -Force -ErrorAction SilentlyContinue
    }
    Remove-Item (Join-Path $identityDir 'identity.json') -Force -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $identityDir 'settings.json') -Force -ErrorAction SilentlyContinue
    $cleared = if ($Hosted) { 'this device identity' } else { 'the server database and this device identity' }
    Write-Host "Cleared $cleared." -ForegroundColor Yellow
}

# A server chosen in the Settings screen is remembered in settings.json, and
# that choice wins over the built-in default, so pointing at the hosted server
# means clearing it. The file holds nothing but the server URL.
if ($Hosted) {
    Remove-Item (Join-Path $identityDir 'settings.json') -Force -ErrorAction SilentlyContinue
}

New-Item -ItemType Directory -Force -Path $binDir, $logDir | Out-Null

# The adapter driver is not in this repository, so it is fetched once.
if (-not (Test-Path (Join-Path $binDir 'wintun.dll'))) {
    & (Join-Path $PSScriptRoot 'fetch-wintun.ps1') -Destination $binDir
}

Write-Host 'Building Go...' -ForegroundColor Cyan
$goTargets = @('./daemon/cmd/192168-daemon')
if (-not $Hosted) { $goTargets += './server/cmd/192168-server' }
Push-Location $repo
try {
    & go build -o "$binDir\" @goTargets
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

# Under -Hosted the daemon resolves the address itself, so this is only for the
# console header. It is read out of the daemon's own default rather than
# repeated here, which is the difference between a header that is true and one
# that is true today.
if ($Hosted) {
    $found = Select-String -Path (Join-Path $repo 'daemon\config\config.go') `
        -Pattern 'DefaultServerURL\s*=\s*"([^"]+)"'
    $serverUrl = if ($found) { $found.Matches[0].Groups[1].Value } else { 'its built-in default' }
}
else {
    $serverUrl = "http://localhost:$Port"
}

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
        [string[]]$Notes,
        [switch]$AsAdmin
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

    $arguments = @('-NoLogo', '-NoExit', '-Command', ($lines -join '; '))

    if ($AsAdmin) {
        # An elevated child cannot be tracked in the pid file the same way, and
        # RunAs refuses to redirect output, which is why the log goes through
        # Tee inside the console instead.
        $console = Start-Process $shell -PassThru -Verb RunAs -ArgumentList $arguments
    }
    else {
        $console = Start-Process $shell -PassThru -ArgumentList $arguments
    }

    Add-Content -Path $pidFile -Value $console.Id
    return $console
}

if ($Hosted) {
    Write-Host "Using the hosted server at $serverUrl" -ForegroundColor Cyan
}
else {
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

    # The daemon registers with the server on its first call, so the server has
    # to be answering before it is worth starting.
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
}

Write-Host 'Starting the daemon' -ForegroundColor Cyan
$adapterNote = if ($Elevated) {
    'elevated, so the virtual adapter can be created'
}
else {
    'not elevated, so no adapter and no game traffic'
}
# An elevated process does not inherit this shell's environment, so anything the
# daemon needs has to be set inside its own console. Under -Hosted the server
# URL is left out entirely, which is what makes the daemon fall back to the
# address it ships with.
$daemonEnvironment = @{ NET192168_LOG_LEVEL = $LogLevel }
if (-not $Hosted) { $daemonEnvironment['NET192168_SERVER_URL'] = $serverUrl }

Start-Console -Title '192168 DAEMON' -Colour 'Cyan' -AsAdmin:$Elevated `
    -Exe (Join-Path $binDir '192168-daemon.exe') `
    -LogFile (Join-Path $logDir 'daemon.log') `
    -Notes @('networking daemon    pipe \\.\pipe\192168', "talking to  $serverUrl", $adapterNote) `
    -Environment $daemonEnvironment | Out-Null

# The daemon does not exist until its console has finished opening, and a cold
# pwsh takes longer than any fixed sleep worth writing. Waiting for the process
# rather than guessing is also what keeps -Hosted honest, since without the
# server console this is the first pwsh to start and the slowest.
$daemonUp = $false
foreach ($attempt in 1..40) {
    Start-Sleep -Milliseconds 250
    if (Get-Process -Name '192168-daemon' -ErrorAction SilentlyContinue) {
        $daemonUp = $true
        break
    }
}
if (-not $daemonUp) { throw 'The daemon never started. Its console will say why.' }

# It can also come up and then fail on its first call to the server, which the
# check above is too early to see.
Start-Sleep -Milliseconds 750
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
Write-Host "  server    $serverUrl$(if ($Hosted) { '  (hosted)' })"
Write-Host "  logs      $logDir"
Write-Host "  crashes   $(Join-Path $identityDir 'client-crash.log')"
Write-Host ''
Write-Host 'Stop everything with: .\scripts\dev.ps1 -Stop' -ForegroundColor DarkGray
if (-not $Elevated) {
    Write-Host 'No virtual adapter. Add -Elevated to carry game traffic.' -ForegroundColor DarkYellow
}
