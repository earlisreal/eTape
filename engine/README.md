# Engine

Enabled real-feed launches automatically start the separate
[tick archive](internal/tickstore/README.md) under `~/.eTape/ticks/`.
Startup/storage failure warns through existing sys.events while market data
continues. Shutdown joins recording producers before draining the archive;
execution SQLite schema and persistence remain independent.

Startup launches pollers while optional per-venue Alpaca asset directories load concurrently. OpenD subscriptions and independent candle/book/ticker/quote seeds can start immediately; only rate-limited requests retain restart cooldowns. OpenD and Alpaca history save atomic request timestamps beside the database to reuse expired windows on ordinary launches and preserve remaining waits on rapid restarts. Unknown state retains the initial 31-second OpenD / one-minute Alpaca history fallback. Level 2 and Time & Sales have no chart-history readiness dependency.

Go process owns external connections, normalized market state, persistence, scanning, execution, and UI transport.

Flow: OpenD/broker/history inputs enter `internal`; `cmd/etape` composes services; `uihub` emits JSON topics and accepts commands. Focused history warms OpenD K_1M then K_DAY first; optional Alpaca/Yahoo providers fill only persisted uncovered ranges before those seams. OpenD cache seeds enter the market-data core losslessly, ticker pushes wait behind an in-flight ticker seed per symbol, and finalized bars archive before the droppable UI update stream. The core preserves Reported Print evidence, stamps condition eligibility once after deduplication, protects bars/marks from ineligible prices, and safely trims finalized 10-second ranges to the authoritative K_1M envelope when the two OpenD streams disagree; execution recovery folds the existing event log into live orders plus a targeted 20:00 ET closed-order projection; the UI-hub mirror publishes both read-only order views. Inputs: TCP/HTTP/WebSocket, config, SQLite. Outputs: UI server, broker requests, durable state, logs.

Invariants: one normalized domain boundary; high-rate paths avoid UI framework state; live orders pass execution gates. Children: [commands](cmd/README.md), [internal packages](internal/README.md), [scripts](scripts/README.md). Test: `go test ./...`; build: `go build ./cmd/etape`.

Scanner Panels share one engine poller, discovery cache, enrichment workers,
subscription warm pool and provider budgets. Each `(workspaceId, panelId)` owns
its filters and sticky board. Session Volume uses one shared page of 200
provider-ranked current-session-volume candidates outside the mover lists.
Closed workspaces release scanner-only warm demand; the panel selected as
Monitoring's Scanner Source stays active while Monitoring is open. OpenD
requests pass through per-family pacing with foreground calls ahead of Scanner
work, and subscription admissions require fresh account-wide quota with
configured headroom. See [external API limits](../docs/external-apis.md) and
the [executed plan](../docs/plans/2026-10-03-independent-scanners.md).

Eligible EXTENDED DAY stop-limits in pre/postmarket remain engine-held until a
fresh Last-Eligible print triggers one linked venue LIMIT child. The engine
persists the parent lifecycle, pauses pretrigger orders across restart/feed
loss, and reconciles uncertain child outcomes without resubmitting. See the
[execution core guide](internal/exec/README.md).

DAY Limit-if-Touched orders are engine-held during the active PRE, RTH, or
POST segment. BUY/COVER triggers at or below the trigger; SELL/SHORT at or
above. A fresh Last-Eligible print creates one linked venue LIMIT child, and
the live-account acknowledgement is separate from stop-limit enablement.

Chart Risk Entry links two engine-held STOP_LIMITs through scheduled DataClose,
sizes shares from a saved risk budget, and protects only confirmed entry fills.
See the [execution core guide](internal/exec/README.md) for partial/late fills,
disarm, cancellation, recovery and phase-checked chart amendments.

## Release builds

The release targets build the UI, embed it, and cross-compile with CGO disabled:

```bash
make release-windows
make release-macos
make release-linux
```

`release-linux` produces `../dist/etape-linux-amd64` for Ubuntu 24.04 and 26.04
LTS on amd64. It is a terminal-launched portable binary: use `-demo` without
OpenD, or configure OpenD separately for live mode. The default browser opens the
local UI; if it cannot be opened, use `http://127.0.0.1:8686`. Ctrl+C stops the
process. The package smoke check lives at
`../scripts/smoke-linux-release.sh` and validates an extracted archive.

Visible-range Volume Profile reads the existing tick archive through a bounded owner-coordinated read-only reader and asynchronous UI query. It adds no market-data subscription or capture-schema change. Recording-disabled sessions can read retained clean sealed history; demo exposes no archive reader. See [tickstore reads](internal/tickstore/README.md#volume-profile-reads) and [UI query behavior](internal/uihub/README.md).
