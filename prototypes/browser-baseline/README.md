# Browser-host baseline

This fixture is the pre-Wails comparison point. It is synthetic and contains
no credentials, account data, or captured runtime data.

Validate the fixture before a run:

```powershell
python prototypes/browser-baseline/record_baseline.py --dry-run
powershell -ExecutionPolicy Bypass -File scripts/check-profile-isolation.ps1
```

On the fixed Windows 11 x64 machine, record the host and display setup, then
run the current browser host from an isolated root:

```powershell
$baselineRoot = Join-Path $env:TEMP "etape-browser-baseline-$PID"
New-Item -ItemType Directory -Force $baselineRoot | Out-Null
$env:ETAPE_PROFILE = "prototype"
$env:ETAPE_DATA_ROOT = $baselineRoot
run.cmd demo 42 -no-open -data-root $baselineRoot
```

Use four browser Workspaces from `fixture.json`, each with the exact twelve-
Panel mix, and enable the existing diagnostic HUD with `?perf=1`. The fixture
names the four Workspace identities, synthetic symbol set, simulated order
intent, and no-real-order boundary. Keep the browser window size, monitor
layout, display scale, refresh rate, and power mode fixed for every run.

The protocol is deterministic: five minutes of warm-up, then three fifteen-
minute runs, sampling once per second. Record one raw JSON result per run with
startup, bridge-to-store, simulated order-intent-to-result, process-tree CPU
and private memory, frame intervals, queue high-water marks, coalesces,
overflows, disconnects, drops, and open/close recovery. Preserve the raw file
beside the fixture outside Git; it is machine-specific evidence, not a stable
repository fixture.

Before the later Wails comparison, repeat the same seed, symbols, four
Workspaces, twelve Panels, display setup, warm-up, run lengths, sample rate,
simulated order intent, and recovery cycles. Compare p95 latency, steady CPU,
private memory, frame intervals, queue behavior, and recovery separately; do
not average away a lossless drop, ordering failure, disconnect, or leaked
Workspace resource.

The user profile is never part of this protocol. A real profile requires the
separate explicit `-profile user -allow-real-profile` opt-in and is outside
baseline or automated validation.
