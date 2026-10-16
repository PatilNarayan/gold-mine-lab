[CmdletBinding()]
param(
    [ValidateSet('1d', '4h', '1h', '15m', '5m', '1m')]
    [string[]]$Timeframes = @('1d', '4h', '1h', '15m', '5m', '1m')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$dataDirectory = Join-Path $repoRoot 'data'
$baseUrl = $env:GML_DATA_BASE_URL
if ([string]::IsNullOrWhiteSpace($baseUrl)) {
    $baseUrl = 'https://github.com/PatilNarayan/gold-mine-lab/releases/download/data-xauusd-v1'
}
$baseUrl = $baseUrl.TrimEnd('/')

New-Item -ItemType Directory -Path $dataDirectory -Force | Out-Null
$temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("gml-data-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temporaryDirectory -Force | Out-Null
$checksumsPath = Join-Path $temporaryDirectory 'SHA256SUMS.txt'

try {
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS.txt" -OutFile $checksumsPath -UseBasicParsing

    $checksums = @{}
    foreach ($line in Get-Content -LiteralPath $checksumsPath) {
        if ([string]::IsNullOrWhiteSpace($line)) {
            continue
        }

        $match = [regex]::Match($line, '^(?<hash>[0-9a-fA-F]{64})\s+\*?(?<name>.+)$')
        if (-not $match.Success) {
            throw "Invalid checksum manifest line: $line"
        }
        $checksums[$match.Groups['name'].Value] = $match.Groups['hash'].Value.ToLowerInvariant()
    }

    foreach ($timeframe in $Timeframes) {
        $csvName = "XAU_${timeframe}_data.csv"
        if (-not $checksums.ContainsKey($csvName)) {
            throw "No checksum found for $csvName in SHA256SUMS.txt"
        }

        $csvPath = Join-Path $dataDirectory $csvName
        $expectedHash = $checksums[$csvName]
        if (Test-Path -LiteralPath $csvPath) {
            $existingHash = (Get-FileHash -LiteralPath $csvPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($existingHash -eq $expectedHash) {
                Write-Host "Skipping $csvName (SHA-256 already verified)"
                continue
            }
            Write-Host "Replacing $csvName (SHA-256 does not match)"
        }

        $archiveName = "XAU_${timeframe}_data.zip"
        $archivePath = Join-Path $dataDirectory $archiveName
        try {
            Invoke-WebRequest -Uri "$baseUrl/$archiveName" -OutFile $archivePath -UseBasicParsing
            Expand-Archive -LiteralPath $archivePath -DestinationPath $dataDirectory -Force

            if (-not (Test-Path -LiteralPath $csvPath)) {
                throw "Archive $archiveName did not contain $csvName at its root"
            }
            $actualHash = (Get-FileHash -LiteralPath $csvPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actualHash -ne $expectedHash) {
                throw "SHA-256 mismatch for ${csvName}: expected $expectedHash, got $actualHash"
            }
            Write-Host "Downloaded and verified $csvName"
        }
        finally {
            if (Test-Path -LiteralPath $archivePath) {
                Remove-Item -LiteralPath $archivePath -Force
            }
        }
    }
}
finally {
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
}
