# Chart history lazy-load plan (viewport-first)

## Problem
Chart currently preloads deep history (1m ~20 trading days, daily to 2016 floor) during demand backfill, then older chunks on left-pan threshold. This over-loads bars beyond visible screen and does not target exact earliest visible time demand.

## Confirmed decisions
- Initial load: viewport-only for both 1m and daily.
- When missing range exceeds chunk size: one API call using required start/end (no chunk loop).
- 1m older than Alpaca 20-trading-day limit: mark exhausted (no older fetch).
- Local 35B ask: plan-only execution workflow, no runtime integration.

## Current state (code map)
- UI trigger path:
  - `ui/src/chrome/panels/ChartPanel.tsx`: subscribes visible logical range; `OlderHistoryController` sends `LoadOlderBars {symbol, daily}`.
  - `ui/src/render/chart/olderHistory.ts`: threshold/cooldown/inflight/exhausted guard, no demanded timestamp in payload.
  - `ui/src/render/chart/ChartController.ts`: preserves viewport on prepends; left pad handling.
- Engine command path:
  - `engine/internal/uihub/commands.go`: handles `LoadOlderBars`, delegates to `Hub.LoadOlder`.
  - `engine/internal/uihub/hub.go`: marshals load-older to injected loader.
- Backfill/data path:
  - `engine/internal/backfill/backfill.go`: warmStart reads archive (1m range + all daily), fill1m/fillDaily archive-first then providers, per-symbol oldest1m watermark and pre-2016 daily one-shot.
  - `engine/internal/backfill/loadolder_test.go`: existing coverage for load-older behavior.
  - `engine/internal/store/bars.go`: `ReadBars1m(symbol, from, to)` and `ReadDailyBars(symbol)` archive access.
  - `engine/internal/hist/alpaca/alpaca.go`: 1m + daily provider.
  - `engine/internal/hist/yahoo/yahoo.go`: daily-only provider.
- Wiring:
  - `engine/cmd/etape/main.go`: injects `SetLoadOlder` to orchestrator `LoadOlder`/`LoadOlderDaily`.

## Proposed approach
1. Move from chunk-by-watermark requests to **demanded-range requests** keyed by earliest visible time.
2. Keep **DB-first** on every request; fetch API only for uncovered slice.
3. Load only enough history to fill current viewport at chart open/timeframe change.
4. Keep per-symbol(+timeframe) earliest-loaded cache in engine and request guards in UI to skip duplicate calls.
5. Apply explicit provider limits/rules:
   - Yahoo daily floor: year 2000; chunk size baseline 5 years.
   - Alpaca 1m floor: last 20 trading days; chunk size baseline 2 days.
   - If missing range > baseline chunk: fetch once with required start/end.

## Implementation todos (narrow, file-scoped)
1. **Contract + transport update**
   - Edit `engine/internal/uihub/wsmsg/payloads.go`: extend `LoadOlderBarsArgs` with demanded range fields (`requiredStartMs`, `requiredEndMs`, and explicit timeframe kind if needed).
   - Regenerate or edit `ui/src/gen/wsmsg.ts` to match contract.
   - Edit `engine/internal/uihub/commands.go`: parse new args and pass through unchanged.
   - Edit `engine/internal/uihub/hub.go`: carry args in `loadOlderReq` and forward into injected `SetLoadOlder` fn signature.

2. **UI demanded-range trigger (initial + pan/zoom)**
   - Edit `ui/src/render/chart/olderHistory.ts`:
     - accept demanded range input,
     - dedupe by `(symbol,timeframe,requiredStartMs)`,
     - preserve inflight/cooldown/exhausted guards.
   - Edit `ui/src/chrome/panels/ChartPanel.tsx`:
     - compute earliest visible time from logical range + loaded bars cache,
     - trigger initial viewport demand on mount/symbol/timeframe switch,
     - send `LoadOlderBars` with demanded range fields.
   - Optional helper touch only if needed: `ui/src/render/chart/ChartController.ts` (read-only cache access already exists via `barsMs()`).

3. **Engine demanded-range loader core**
   - Edit `engine/internal/backfill/backfill.go`:
     - replace watermark-only `LoadOlder` flow with demanded-range aware flow,
     - add per-symbol+timeframe earliest-loaded cache to skip duplicate covered calls,
     - keep archive-first coverage check before provider call.
   - Edit `engine/cmd/etape/main.go`:
     - update `SetLoadOlder` injection signature and bridge to orchestrator demanded-range methods.

4. **Provider rules + hard floors**
   - Edit `engine/internal/backfill/backfill.go` range policy:
     - Yahoo daily floor hard-set to `2000-01-01`,
     - Alpaca 1m floor hard-set to last 20 trading days (exhausted if requested older),
     - baseline windows 5y daily / 2d 1m,
     - when demanded missing range exceeds baseline, call provider once with demanded start/end.
   - Keep provider clients unchanged unless tests show helper needed:
     - `engine/internal/hist/yahoo/yahoo.go`
     - `engine/internal/hist/alpaca/alpaca.go`

5. **Boot backfill realignment**
   - Edit `engine/internal/backfill/backfill.go` `Backfill()/warmStart/fill*` path:
     - stop preloading beyond viewport demand for chart surfaces,
     - retain minimal warm data needed for non-chart behavior.
   - Keep compatibility path for replay/no-provider mode in `engine/cmd/etape/main.go`.

6. **Tests (only touched surfaces)**
   - UI tests:
     - `ui/src/render/chart/olderHistory.test.ts`
     - `ui/src/chrome/panels/ChartPanel.test.tsx`
   - Engine tests:
     - `engine/internal/uihub/commands_test.go`
     - `engine/internal/uihub/hub_demand_test.go` (or closest load-older hub tests)
     - `engine/internal/backfill/loadolder_test.go`
     - `engine/internal/hist/yahoo/yahoo_test.go`
     - `engine/internal/hist/alpaca/alpaca_test.go`

## Notes / risks
- Biggest behavior shift: replacing deep preload with viewport-first flow. Guard with explicit tests around initial chart usability and no-data/exhausted responses.
- Daily archive read currently returns all bars; plan may need range read helper in store for efficient coverage checks if full-scan becomes hot.

## Local LLM 35B execution runbook (explicit batches)
### Batch 1 — Contract + UI wire-up only (DONE)
- Files allowed:
  - `engine/internal/uihub/wsmsg/payloads.go`
  - `engine/internal/uihub/commands.go`
  - `engine/internal/uihub/hub.go`
  - `ui/src/gen/wsmsg.ts`
  - `ui/src/render/chart/olderHistory.ts`
  - `ui/src/chrome/panels/ChartPanel.tsx`
- Stop condition:
  - UI sends demanded range fields; engine accepts and forwards fields to loader callback.
- Validate:
  - `cd ui && npm test -- olderHistory ChartPanel`
  - `cd engine && go test ./internal/uihub -run LoadOlder`

### Batch 2 — Backfill/range policy only
- Files allowed:
  - `engine/internal/backfill/backfill.go`
  - `engine/cmd/etape/main.go`
  - (only if needed) `engine/internal/store/bars.go`
- Stop condition:
  - DB-first coverage + demanded-range fetch flow implemented.
  - Yahoo floor=2000, Alpaca 1m floor=last 20 trading days, chunk baseline and single-call override rules active.
- Validate:
  - `cd engine && go test ./internal/backfill -run LoadOlder`

### Batch 3 — Provider-edge tests + cleanup
- Files allowed:
  - `engine/internal/hist/yahoo/yahoo_test.go`
  - `engine/internal/hist/alpaca/alpaca_test.go`
  - `engine/internal/uihub/commands_test.go`
  - `ui/src/render/chart/olderHistory.test.ts`
  - `ui/src/chrome/panels/ChartPanel.test.tsx`
- Stop condition:
  - New behavior covered for exhausted floors, demanded-range pass-through, and dedupe.
- Validate:
  - `cd engine && go test ./internal/hist/yahoo ./internal/hist/alpaca ./internal/uihub`
  - `cd ui && npm test -- olderHistory ChartPanel`
