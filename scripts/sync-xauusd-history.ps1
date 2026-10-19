# sync-xauusd-history.ps1 — incremental XAUUSD history refresh from Twelve Data.
#
# Produces a NEW versioned dataset directory (never touches data/). Original
# CSVs are copied verbatim, then missing candles are appended after the last
# accepted timestamp in the repository's broker-time convention (EET/EEST).
#
# Requires TWELVE_DATA_API_KEY in the environment or backend/.env.
# Select the snapshot at run time: MARKET_DATA_DIR=data-twelvedata-v2
#
# Examples:
#   .\scripts\sync-xauusd-history.ps1                       # incremental sync
#   .\scripts\sync-xauusd-history.ps1 -DryRun               # preview only
#   .\scripts\sync-xauusd-history.ps1 -Out data-twelvedata-v3 -Timeframes 15m,1h
param(
    [string]$Src = "data",
    [string]$Out = "data-twelvedata-v2",
    [string]$Timeframes = "1d,4h,1h,15m",
    [int]$MaxRequests = 40,
    [string]$RequestInterval = "8s",
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo
try {
    # Load TWELVE_DATA_API_KEY from backend/.env if not already exported.
    $envFile = Join-Path $repo 'backend\.env'
    if ((Test-Path $envFile) -and -not $env:TWELVE_DATA_API_KEY) {
        Get-Content $envFile | ForEach-Object {
            if ($_ -match '^\s*([A-Z_]+)\s*=\s*(.*)$') {
                Set-Item -Path "env:$($Matches[1])" -Value $Matches[2]
            }
        }
    }
    $args = @('run', './cmd/datasync', '-src', $Src, '-out', $Out,
        '-timeframes', $Timeframes, '-max-requests', "$MaxRequests",
        '-request-interval', $RequestInterval)
    if ($DryRun) { $args += '-dry-run' }
    & go @args
    exit $LASTEXITCODE
}
finally { Pop-Location }
