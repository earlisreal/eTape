# Export trades → eJournal (design)

## Context

Earl runs a paper "simulator" (`sim` venue) inside eTape whose fills exist **nowhere
else** — no broker can reproduce them. He wants to analyze those trades in **eJournal**
(his separate Kotlin/Compose journal app, `~/Projects/eJournal`). eJournal has **no
HTTP API, no server, no port** — it's a local SQLite app that imports **per-fill
BUY/SELL transactions** via CSV and computes round-trips/P&L itself (FIFO). So the
integration is a **file export from eTape** that eJournal imports.

eTape already persists every fill (sim included) in SQLite (`~/.eTape/etape.db`, table
`fills`), and this export was explicitly designed for: the portfolio spec calls out that
"eJournal's unique `externalId` enables a trivial future export"
(`docs/superpowers/specs/2026-07-03-portfolio-orders-design.md:25`).

## Decisions

- **CSV carrying a per-fill `externalId`** so re-imports are idempotent. Earl builds the
  matching eTape parser in eJournal in a **separate session** — that work is out of
  scope for eTape.
- **Export the currently-selected venue** (the AccountPanel header already has a venue
  `<select>`). Works for `sim`, `alpaca`, `alpaca-live`, `tz`, `moomoo` — the
  `externalId` encodes the venue. `fees=0` is correct for sim **and** commission-free
  Alpaca equities.
- **Date filter**: presets (Today / This week / This month / All time) + custom
  From/To, default All time. Ranges are **ET calendar** ranges, resolved engine-side
  (`session.BucketStartMs`) so "today/week/month" stay in agreement with the rest of
  the engine's session logic.
- **UI**: a popover opened from an Export button in the Account panel header, beside
  the venue select.

## The eJournal CSV contract (cross-app interface — the eJournal-side session needs this)

Header row, then one row per fill. **Column order is eJournal's Generic-importer
positional order (indices 0–5) with `externalId` appended at index 6.** This means the
file imports **today** via eJournal's existing Generic importer (reads columns 0–5
positionally, ignores index 6, no dedup) and becomes **idempotent** once a dedicated
eTape parser in eJournal reads the trailing `externalId` column (eJournal's
`TradeTransaction.externalId` has a unique index; `INSERT OR IGNORE` collapses repeats).

```
datetime,symbol,action,price,shares,fees,externalId
2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:sim:12
2026-07-10T09:44:02,NVDA,SELL,121.25,100,0,etape:sim:19
```

| Col | Field | Source / rule |
|-----|-------|---------------|
| 0 | `datetime` | fill `TsMs` (epoch ms) → **America/New_York** wall-clock, ISO local **no zone**, seconds precision (Go `2006-01-02T15:04:05`). eJournal's `LocalDateTime.parse` accepts this. |
| 1 | `symbol` | fill symbol with `US.` stripped via `strings.TrimPrefix(s,"US.")` (preserves class shares: `US.BRK.B`→`BRK.B`). |
| 2 | `action` | `BUY` for exec sides `BUY`/`COVER`; `SELL` for `SELL`/`SHORT`. |
| 3 | `price` | fill price, per share. |
| 4 | `shares` | fill qty (positive). |
| 5 | `fees` | literal `0` (eTape tracks none; correct for sim + commission-free equities). |
| 6 | `externalId` | `etape:{venue}:{fillId}` (`fillId` = `fills` table PK). Stable + unique per fill. |

The date filter narrows which rows are emitted; it does not change this contract.

## Architecture

- **Store** (`engine/internal/store/exec.go`): new `ExportFills(ctx, venue, fromMs, toMs)`
  reads the existing `fills` table (no schema change — `fill_id` already exists),
  venue- and range-scoped, carrying the PK.
- **Engine helpers** (new `engine/internal/exec/export.go`, package `exec`, pure/testable):
  `ResolveExportRange(preset, from, to, now)` turns a preset or custom range into
  `[fromMs, toMs)`; `BuildFillsCSV(rows)` renders the CSV above via `encoding/csv`.
- **Wire** (`engine/internal/uihub/wsmsg/payloads.go` + `query.go`): a new `ExportFills`
  WS query (args: venue/preset/from/to; result: `{csv, count}`), following the existing
  `QueryFills` query-channel pattern. The query handler is given the engine clock so
  presets resolve against `clk.Now()` (real time live, the replay day under `-replay`).
- **UI**: `ExportTradesPopover` (new component), triggered by an Export button portaled
  into the Account panel's header actions slot beside the venue select. Presets + a
  custom date-range fallback; Download builds a `Blob` and triggers a browser download
  via the same anchor-click idiom as the chart screenshot feature.

## Out of scope

- The eJournal-side parser that reads the `externalId` column for dedup — separate
  session, separate repo.
- Fees/commission tracking in eTape generally (there is none today).
- Chunked/HTTP delivery for very large histories — the whole CSV rides one WS result
  frame, which is fine at realistic volumes.
