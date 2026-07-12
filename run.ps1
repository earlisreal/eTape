# Convenience launcher for eTape on Windows. Mirrors run.sh. Three modes:
#   live  - real engine against %USERPROFILE%\.eTape\config.toml (live OpenD feed + venues)
#   demo  - real engine against a live synthetic market (no OpenD/broker needed)
#   dev   - mock WS engine + Vite dev server, hot reload for UI work
#
# Prefer invoking via run.cmd (it sets the PowerShell execution policy for you).
# Direct: powershell -ExecutionPolicy Bypass -File run.ps1 <mode> [options]
$ErrorActionPreference = 'Stop'

$Root      = $PSScriptRoot
$EngineDir = Join-Path $Root 'engine'
$UIDir     = Join-Path $Root 'ui'
$Dist      = Join-Path $UIDir 'dist'

function Log($msg) { [Console]::Error.WriteLine("run.ps1: $msg") }

function Show-Usage {
    Write-Output @'
Usage: run.cmd <mode> [options]     (or: powershell -File run.ps1 <mode> [options])

Modes:
  live               Build the UI, then run the real engine against
                     %USERPROFILE%\.eTape\config.toml (live OpenD feed + real
                     venues). Requires OpenD already running and logged in. Extra
                     args are passed through to the engine, e.g.:
                       run.cmd live -no-open -log C:\temp\etape.log

  demo [SEED]        Build the UI, then run the engine against a live
                     synthetic market -- no OpenD or broker required. A
                     random universe/seed is drawn per launch; pass SEED to
                     pin it to the same reproducible universe/day.

  dev [FIXTURE]      Run the mock WS engine + Vite dev server with hot
                     reload, for UI iteration. FIXTURE selects a
                     ui\fixtures\<name>.json (default: session-basic).

Examples:
  run.cmd live
  run.cmd demo
  run.cmd demo 42
  run.cmd dev ladder-tape
'@
}

function Build-UI {
    Log 'building UI bundle'
    Push-Location $UIDir
    try {
        & npm run build
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    } finally {
        Pop-Location
    }
}

# Parse args WITHOUT a param() block so engine flags like -log=... pass
# through verbatim (PowerShell parameter binding would try to interpret them).
$Mode = ''
$Rest = @()
if ($args.Count -ge 1) { $Mode = [string]$args[0] }
if ($args.Count -ge 2) { $Rest = @($args[1..($args.Count - 1)]) }

switch ($Mode) {
    'live' {
        Build-UI
        Log 'booting engine (live) -- open http://127.0.0.1:8686'
        Set-Location $EngineDir
        & go run ./cmd/etape -dist $Dist @Rest
        exit $LASTEXITCODE
    }

    'demo' {
        # Optional leading positional SEED (any non-flag first arg); everything
        # else (e.g. -no-open, -log ...) passes through to the engine untouched.
        $seed = ''
        if ($Rest.Count -ge 1 -and [string]$Rest[0] -notlike '-*') {
            $seed = [string]$Rest[0]
            $Rest = if ($Rest.Count -ge 2) { @($Rest[1..($Rest.Count - 1)]) } else { @() }
        }

        Build-UI

        Log 'booting engine (demo synthetic market) -- open http://127.0.0.1:8686'
        Set-Location $EngineDir
        if ($seed) {
            & go run ./cmd/etape -dist $Dist -demo -demo-seed $seed @Rest
        } else {
            & go run ./cmd/etape -dist $Dist -demo @Rest
        }
        exit $LASTEXITCODE
    }

    'dev' {
        $fixture = if ($Rest.Count -ge 1) { [string]$Rest[0] } else { 'session-basic' }
        Set-Location $UIDir

        Log "starting mock engine (fixture: $fixture)"
        # Launch via cmd.exe so npm resolves without guessing npm.cmd vs npm.ps1,
        # and so taskkill /T can tear down the whole cmd -> npm -> node tree.
        $mock = Start-Process -FilePath 'cmd.exe' `
            -ArgumentList @('/c', 'npm', 'run', 'mock-engine', '--', $fixture) `
            -NoNewWindow -PassThru

        $code = 1
        try {
            Log 'starting Vite dev server'
            & npm run dev
            $code = $LASTEXITCODE
        } finally {
            if ($mock -and -not $mock.HasExited) {
                Log "stopping mock engine (pid $($mock.Id))"
                & taskkill /T /F /PID $mock.Id 2>$null | Out-Null
            }
        }
        exit $code
    }

    { $_ -in @('', '-h', '--help', 'help') } {
        Show-Usage
        exit 0
    }

    default {
        Log "unknown mode '$Mode'"
        Show-Usage
        exit 1
    }
}
