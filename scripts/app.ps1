#Requires -Version 5.1
<#
.SYNOPSIS
  Starts and stops the Gold Mine Lab application stack.

.DESCRIPTION
  One script to manage the whole local dev stack:
    - Postgres        (docker compose service in backend/)
    - Backend API     (Go server on :8080, runs bin/server.exe)
    - Frontend        (Vite dev server on :5173)

  PIDs and log locations are tracked in scripts/.run/state.json so `stop`
  can cleanly kill each process tree (including child processes spawned
  by `go run`-style launchers and npm scripts).

.EXAMPLES
  .\scripts\app.ps1 start            # start DB + backend + frontend (API mode)
  .\scripts\app.ps1 start -Build     # force-rebuild the backend binary first
  .\scripts\app.ps1 start -Mock      # frontend uses built-in demo data, no API
  .\scripts\app.ps1 start -NoDb      # skip docker postgres (e.g. external DB)
  .\scripts\app.ps1 stop             # stop backend + frontend (DB keeps running)
  .\scripts\app.ps1 stop -StopDb     # also stop the postgres container
  .\scripts\app.ps1 restart
  .\scripts\app.ps1 status
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status')]
    [string]$Action = 'start',

    # Rebuild backend\bin\server.exe even if it already exists.
    [switch]$Build,

    # Do not start the docker postgres container on `start`.
    [switch]$NoDb,

    # On `stop`, also run `docker compose down` for the postgres container.
    [switch]$StopDb,

    # Run the frontend against deterministic mock data instead of the API.
    [switch]$Mock,

    [int]$BackendPort = 8080,
    [int]$FrontendPort = 5173
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Root        = Split-Path -Parent $PSScriptRoot
$BackendDir  = Join-Path $Root 'backend'
$FrontendDir = Join-Path $Root 'frontend'
$ComposeFile = Join-Path $BackendDir 'docker-compose.yml'
$ServerExe   = Join-Path $BackendDir 'bin\server.exe'
$ViteBin     = Join-Path $FrontendDir 'node_modules\vite\bin\vite.js'
$RunDir      = Join-Path $PSScriptRoot '.run'
$LogDir      = Join-Path $RunDir 'logs'
$StateFile   = Join-Path $RunDir 'state.json'

function Write-Step([string]$Msg) { Write-Host "==> $Msg" -ForegroundColor Cyan }
function Write-Info([string]$Msg) { Write-Host "    $Msg" }
function Write-Warn([string]$Msg) { Write-Host "    $Msg" -ForegroundColor Yellow }

function Assert-Command([string]$Name, [string]$Hint) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "'$Name' not found on PATH. $Hint"
    }
}

function Import-BackendEnv {
    # Load backend/.env into this process env (children inherit it), then apply
    # required defaults for anything still unset.
    $envFile = Join-Path $BackendDir '.env'
    if (Test-Path $envFile) {
        Write-Info "Loading $envFile"
        foreach ($line in Get-Content $envFile) {
            $line = $line.Trim()
            if (-not $line -or $line.StartsWith('#') -or $line -notmatch '=') { continue }
            $key, $value = $line -split '=', 2
            $key = $key.Trim(); $value = $value.Trim()
            [Environment]::SetEnvironmentVariable($key, $value, 'Process')
        }
    }
    if (-not $env:DATABASE_URL) {
        $env:DATABASE_URL = 'postgres://goldmine:goldmine@localhost:5433/goldmine?sslmode=disable'
    }
    # MARKET_DATA_DIR removed — historical candles sync from the configured
    # provider into Postgres; no bundled CSV datasets at runtime.
    if (-not $env:HTTP_PORT) { $env:HTTP_PORT = "$BackendPort" }
}

function Get-PostgresHealth {
    try {
        return (docker inspect --format '{{.State.Health.Status}}' goldmine-postgres 2>$null).Trim()
    } catch { return '' }
}

function Start-Postgres {
    Write-Step 'Starting Postgres (docker compose)'
    Assert-Command docker 'Install Docker Desktop and try again.'
    docker compose -f $ComposeFile up -d postgres | Out-Null

    Write-Info 'Waiting for postgres to become healthy...'
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        if ((Get-PostgresHealth) -eq 'healthy') { Write-Info 'postgres: healthy'; return }
        Start-Sleep -Seconds 2
    }
    Write-Warn 'postgres did not report healthy within 60s - backend may fail to connect.'
}

function Build-Backend {
    Write-Step 'Building backend (go build -o bin/server.exe ./cmd/server)'
    Assert-Command go 'Install Go 1.23+ and try again.'
    Push-Location $BackendDir
    try { go build -o bin\server.exe ./cmd/server } finally { Pop-Location }
}

function Test-PortListening([int]$Port) {
    try {
        return [bool](Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction Stop)
    } catch { return $false }
}

function Wait-ForPort([int]$Port, [string]$Name, [int]$TimeoutSec = 30) {
    Write-Info "Waiting for $Name on :$Port..."
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Test-PortListening $Port) { Write-Info "$Name is up on :$Port"; return $true }
        Start-Sleep -Milliseconds 500
    }
    Write-Warn "$Name did not open :$Port within ${TimeoutSec}s - check $LogDir"
    return $false
}

function Get-PidsOnPort([int]$Port) {
    try {
        Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction Stop |
            Select-Object -ExpandProperty OwningProcess -Unique
    } catch { @() }
}

function Save-State($State) {
    New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
    $State | ConvertTo-Json | Set-Content -Path $StateFile -Encoding UTF8
}

function Load-State {
    if (Test-Path $StateFile) {
        try { return Get-Content $StateFile -Raw | ConvertFrom-Json } catch { return $null }
    }
    return $null
}

function Stop-ProcessTree([int]$ProcessId, [string]$Name) {
    $proc = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    if (-not $proc) { Write-Info "$Name (pid $ProcessId) is not running"; return }
    Write-Info "Stopping $Name (pid $ProcessId) and children"
    # /T kills the whole tree - covers child processes (e.g. go run -> server).
    & taskkill.exe /PID $ProcessId /T /F 2>&1 | Out-Null
}

function Start-Backend {
    Write-Step 'Starting backend'
    if ($Build -or -not (Test-Path $ServerExe)) { Build-Backend }

    Import-BackendEnv
    $out = Join-Path $LogDir 'backend.out.log'
    $err = Join-Path $LogDir 'backend.err.log'
    $p = Start-Process -FilePath $ServerExe -WorkingDirectory $BackendDir `
        -RedirectStandardOutput $out -RedirectStandardError $err -PassThru
    Write-Info "backend pid $($p.Id), logs: $out"
    Wait-ForPort -Port $BackendPort -Name 'backend' | Out-Null
    return $p.Id
}

function Start-Frontend {
    Write-Step 'Starting frontend (vite)'
    Assert-Command node 'Install Node.js and try again.'

    # Process env wins over frontend/.env in Vite, so the launcher decides the
    # data source: real Go API by default, deterministic mocks with -Mock.
    if (-not $env:VITE_DATA_MODE) {
        $env:VITE_DATA_MODE = if ($Mock) { 'mock' } else { 'api' }
    }
    if (-not $env:VITE_API_URL) { $env:VITE_API_URL = "http://localhost:$BackendPort" }
    Write-Info "VITE_DATA_MODE=$env:VITE_DATA_MODE  VITE_API_URL=$env:VITE_API_URL"

    if (-not (Test-Path $ViteBin)) {
        Write-Info 'node_modules/vite not found - running npm install'
        Push-Location $FrontendDir
        try { npm install } finally { Pop-Location }
    }
    $out = Join-Path $LogDir 'frontend.out.log'
    $err = Join-Path $LogDir 'frontend.err.log'
    $p = Start-Process -FilePath 'node.exe' -ArgumentList "`"$ViteBin`"" `
        -WorkingDirectory $FrontendDir `
        -RedirectStandardOutput $out -RedirectStandardError $err -PassThru
    Write-Info "frontend pid $($p.Id), logs: $out"
    Wait-ForPort -Port $FrontendPort -Name 'frontend' | Out-Null
    return $p.Id
}

function Invoke-Start {
    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

    if (-not $NoDb) { Start-Postgres }

    $state = [ordered]@{ startedAt = (Get-Date).ToString('o') }

    if (Test-PortListening $BackendPort) {
        Write-Warn "port $BackendPort is already in use - skipping backend start"
        $state.backendPid = $null
    } else {
        $state.backendPid = Start-Backend
    }

    if (Test-PortListening $FrontendPort) {
        Write-Warn "port $FrontendPort is already in use - skipping frontend start"
        $state.frontendPid = $null
    } else {
        $state.frontendPid = Start-Frontend
    }

    Save-State $state
    Write-Step 'Done'
    Write-Info "Frontend : http://localhost:$FrontendPort"
    Write-Info "Backend  : http://localhost:$BackendPort/api/health"
    Write-Info "Logs     : $LogDir"
    Write-Info "Stop with: .\scripts\app.ps1 stop"
}

function Invoke-Stop {
    $state = Load-State
    $stopped = $false

    if ($state) {
        if ($state.backendPid)  { Stop-ProcessTree $state.backendPid 'backend';  $stopped = $true }
        if ($state.frontendPid) { Stop-ProcessTree $state.frontendPid 'frontend'; $stopped = $true }
    }

    # Fallback: kill whatever still owns the ports (covers lost state file).
    foreach ($pair in @(@($BackendPort, 'backend'), @($FrontendPort, 'frontend'))) {
        foreach ($processId in (Get-PidsOnPort $pair[0])) {
            Stop-ProcessTree $processId $pair[1]
            $stopped = $true
        }
    }

    if (-not $stopped) { Write-Info 'Nothing to stop.' }

    if ($StopDb) {
        Write-Step 'Stopping Postgres (docker compose down)'
        docker compose -f $ComposeFile down | Out-Null
    }

    if (Test-Path $StateFile) { Remove-Item $StateFile -Force }
    Write-Step 'Stopped'
}

function Invoke-Status {
    $state = Load-State
    $rows = @()
    foreach ($name in @('backendPid', 'frontendPid')) {
        $procId = if ($state) { $state.$name } else { $null }
        $alive = $procId -and (Get-Process -Id $procId -ErrorAction SilentlyContinue)
        $rows += [pscustomobject]@{
            Component = $name -replace 'Pid', ''
            PID       = if ($procId) { $procId } else { '-' }
            Running   = [bool]$alive
        }
    }
    $pgHealth = Get-PostgresHealth
    $rows += [pscustomobject]@{ Component = 'postgres'; PID = 'docker'; Running = ($pgHealth -eq 'healthy' -or $pgHealth -eq 'running') }
    $rows | Format-Table -AutoSize
    Write-Info "backend :$BackendPort listening: $(Test-PortListening $BackendPort)"
    Write-Info "frontend :$FrontendPort listening: $(Test-PortListening $FrontendPort)"
}

switch ($Action) {
    'start'   { Invoke-Start }
    'stop'    { Invoke-Stop }
    'restart' { Invoke-Stop; Invoke-Start }
    'status'  { Invoke-Status }
}
