param(
    [string]$RepositoryRoot = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = 'Stop'
$baselineDir = Join-Path $RepositoryRoot 'prototypes\browser-baseline'
$realRoot = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE '.eTape')).TrimEnd('\')
$sensitiveKey = '(?i)"(?:secret|password|credential|api[_-]?key|account(?:[_-]?id)?|access[_-]?token|private[_-]?key)"\s*:'

Get-ChildItem -LiteralPath $baselineDir -Recurse -File |
    Where-Object { $_.Extension -in '.json', '.jsonl', '.csv', '.log' } |
    ForEach-Object {
        $full = $_.FullName
        $underRealProfile = $full.Equals($realRoot, [StringComparison]::OrdinalIgnoreCase) -or
            $full.StartsWith($realRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)
        if ($underRealProfile) {
            throw "baseline artifact is under the real user profile: $full"
        }
        $raw = Get-Content -Raw -LiteralPath $full
        if ($raw -match $sensitiveKey) {
            throw "sensitive key found in baseline artifact: $full"
        }
    }

$cacheDir = Join-Path ([IO.Path]::GetTempPath()) "etape-profile-check-$PID"
Push-Location (Join-Path $RepositoryRoot 'engine')
try {
    $env:GOCACHE = $cacheDir
    go test ./internal/profile
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Pop-Location
    Remove-Item -LiteralPath $cacheDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output 'profile isolation and baseline redaction checks passed'
