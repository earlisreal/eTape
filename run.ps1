# Convenience launcher for eTape on Windows. Mirrors run.sh. Three modes:
#   live  - real engine against %USERPROFILE%\.eTape\config.toml (live OpenD feed + venues)
#   demo  - real engine against a synthetic replay day (no OpenD/broker needed)
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
                     venues). Requires OpenD already running and unlocked. Extra
                     args are passed through to the engine, e.g.:
                       run.cmd live -watch=AAPL,TSLA -focus=AAPL

  demo [DAY] [SPEED] Build the UI, generate a synthetic replay day, and run
                     the engine against it. No OpenD or broker required.
                     DAY defaults to 2026-01-02. SPEED defaults to 1
                     (real-time); use 0 to replay as fast as possible.

  dev [FIXTURE]      Run the mock WS engine + Vite dev server with hot
                     reload, for UI iteration. FIXTURE selects a
                     ui\fixtures\<name>.json (default: session-basic).

Examples:
  run.cmd live
  run.cmd live -watch=AAPL,TSLA
  run.cmd demo
  run.cmd demo 2026-01-02 0
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

# Parse args WITHOUT a param() block so engine flags like -focus=AAPL pass
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
        $day   = if ($Rest.Count -ge 1) { [string]$Rest[0] } else { '2026-01-02' }
        $speed = if ($Rest.Count -ge 2) { [string]$Rest[1] } else { '1' }

        $work = Join-Path ([System.IO.Path]::GetTempPath()) ('etape-demo-' + [System.Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $work | Out-Null
        $db  = Join-Path $work 'demo.db'
        $cfg = Join-Path $work 'demo.toml'

        Build-UI

        Log "generating synthetic journal ($db)"
        Set-Location $EngineDir
        & go run ./cmd/genjournal -db $db -day $day
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

        # db_path is a TOML literal string (single quotes) so backslashes in the
        # Windows temp path are not treated as escape sequences.
        $toml = @"
[store]
db_path = '$db'
[uihub]
host = "127.0.0.1"
port = 8686
[[venue]]
id = "sim-paper"
broker = "sim"
env = "paper"
[gate.global]
max_day_loss = 100000
max_symbol_position_value = 100000
max_symbol_position_shares = 100000
[gate.venue.sim-paper]
max_order_value = 100000
max_position_value = 100000
max_position_shares = 100000
max_open_orders = 50
"@
        # UTF-8 without BOM: a leading BOM can break TOML section parsing.
        [System.IO.File]::WriteAllText($cfg, $toml, (New-Object System.Text.UTF8Encoding($false)))

        Log "booting engine (replay $day, speed $speed) -- open http://127.0.0.1:8686"
        & go run ./cmd/etape -config $cfg -replay $day -speed $speed -replay-hold -dist $Dist
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
