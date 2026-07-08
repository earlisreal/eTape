# Plan: whole-app "refresh" when a new mover appears / notification sound fires

## Context

**Symptom (user-reported):** When a new stock enters the movers list and the notification
chime plays, the **whole app** visibly refreshes on its own (chart + all panels flicker/
redraw) — no user interaction involved.

**Root-cause chain (established by code trace, not to be re-derived):**
1. The only thing in the whole UI that can visibly change *all* panels at once is
   `ReconnectOverlay` (`ui/src/chrome/ReconnectOverlay.tsx`): it dims the entire app to 40%
   opacity and shows a "connecting…/reconnecting…" layer whenever the WS connection state
   isn't `"open"` (state lives in `App.tsx`, fed by `client.onState(setState)`). The sound
   is pure imperative Web Audio (touches no React state) and scanner data only re-renders
   the Scanner/Movers panel itself — neither can cause a whole-app redraw on its own.
2. So the "whole-app refresh" **is a WebSocket disconnect → reconnect.** On reconnect the
   client re-subscribes every topic, the engine re-sends snapshots, the movers list
   repopulates ("new stocks") and a following delta can fire the chime. Movers/sound are
   *symptoms* of the reconnect, not the cause.
3. The disconnect is **by design**: `engine/internal/uihub` force-closes any client whose
   outbound queue overflows (`conn.go`'s `enqueue` calls `c.close()` on a full channel;
   `hub.go`'s `broadcast`/`sendSnapshot` drop any client whose `enqueue` returns false).
   The config comment says so explicitly: `outbound_queue … overflow => drop + force
   re-snapshot` (`internal/config/config.go`). During busy market activity (exactly when
   movers churn), a browser render/GC stall lets the client fall behind the 1024-frame
   buffer, the socket is dropped, and recovery is the jarring full-app re-sync.

**Intended outcome:** backpressure becomes *recoverable* — a slow client sheds stale
"latest-wins" frames instead of losing the whole connection — and the residual reconnect
UX no longer flashes the entire app for a sub-second blip. Event frames (ticks, orders,
fills, sys events) are never dropped.

## Global Constraints (binding on every task below)

- `enqueue` is called from the Hub's single `Run` goroutine (inside `broadcast` and
  `sendSnapshot` in `hub.go`). It **must stay non-blocking / bounded-time** — blocking it
  would stall every connected client for one slow client. All shedding/coalescing logic
  must live in the conn's own goroutine space (the writer goroutine + a mutex-guarded
  struct), never inside the Hub's `Run` loop.
- Preserve the existing single-writer discipline: exactly one goroutine (`writeLoop`) may
  ever call `ws.Write`. Any new structure must remain `go test -race` clean.
- Event/ordered topics must never be silently dropped by load-shedding: `md.tape`,
  `exec.orders`, `exec.fills`, `exec.status`, `sys.events`, `news.item`, `scanner.hit`,
  `config`, `md.indicator`, and **all snapshots regardless of topic**. Only genuinely
  "latest-wins" deltas may be coalesced/superseded: `md.quote`, `md.book`, `md.bars`
  (existing `dedupOf` key), `exec.account` (per venue), `exec.positions` (single slot),
  `scanner.rank` (per session key), `sys.health` (single slot).
- Per-topic delivery order must be preserved (event frames strictly FIFO; a coalesced
  key's queued position is set by its *first* enqueue, later updates overwrite the payload
  in place rather than moving to the back). Cross-topic reordering is fine — each topic
  feeds an independent store on the UI side.
- A snapshot for a topic/client must always be deliverable before any later delta for that
  same topic/client — this already holds structurally (a subscribe's snapshot is enqueued
  synchronously in `handleSub` before any later `broadcast` call for that client) and must
  not be broken by any change here.
- Follow this repo's existing Go test conventions: table-driven where natural, fakes over
  mocks, `go test -race` must stay green for every package touched.
- Do not touch `ScannerPanel.tsx`, `ScannerStore.ts`, or `AppShell.tsx` — the trace
  ruled them out as the cause; changing them is out of scope for this plan.

## Task 1: Engine — write-deadline on writeLoop + drop instrumentation as sys.events

**Goal:** make every client drop visible (so the diagnosis is confirmed against a live
session) and turn a genuinely wedged (not just slow) socket into a prompt, clean drop
instead of an indefinitely blocked writer goroutine.

**Files:** `engine/internal/uihub/conn.go`, `engine/internal/uihub/hub.go`,
`engine/internal/uihub/server.go`, `engine/cmd/etape/main.go`.

**Requirements:**

1. In `conn.go`'s `writeLoop`, wrap each `c.ws.Write(ctx, b)` call in a
   `context.WithTimeout(ctx, c.writeTimeout)` (call `cancel()` after each write). Add a
   `writeTimeout time.Duration` field to `conn`, set by `newConn`'s caller. Default to a
   generous value (5 seconds) in production wiring (`server.go`'s `serveWS`); allow tests
   to construct a `conn` with a short timeout directly. A write that times out must close
   the connection via the existing `c.close()` path.
2. Every place a client is dropped because its outbound path failed — `enqueue` returning
   false inside `broadcast`, `enqueue` returning false inside `sendSnapshot`, and the new
   write-timeout path in `writeLoop` — must result in one `sys.events` entry with
   `Kind: "ui-drop"` and a `Detail` string identifying the reason (e.g. `"dropped UI client
   <id>: outbound queue overflow"` / `"... write timeout"`). Reuse the existing
   `wsmsg.SysEvent{Seq, Ts, Kind, Detail}` struct (`engine/internal/uihub/wsmsg/payloads.go`)
   and the same publish path the engine already uses for sys.events, so the event lands in
   `HealthStore` on the UI side and renders in `ConnectionStatusPanel.tsx` without any UI
   change. Look at `engine/internal/health/health.go`'s `Poller.Event(kind, detail string)`
   method for the exact pattern (sequence numbering + timestamp format + `Publish` call) —
   match its shape but do not couple to the `health.Poller` type itself; the Hub needs its
   own equivalent since a drop is detected inside `Hub.Run`, not the health poller.
3. **Concurrency requirement:** the Hub's `Run` goroutine detects the drop (inside
   `broadcast`/`sendSnapshot`) and must not block emitting the event. Do not call
   `Hub.Publish` (which sends on `h.pubCh`, itself drained by `Run` — a self-send from
   inside `Run` risks deadlock if that buffered channel is ever full). Instead, emit the
   sys.events frame the same way `handlePub` does internally: build the `staged` value,
   apply it to the mirror directly, and broadcast it to survivors, from within `Run`'s own
   goroutine (i.e., add a Hub method that does what `handlePub` does but is invoked
   directly, not via the channel). Guard against reentrancy: if emitting this event itself
   causes another client's `enqueue` to fail (a further drop), collect all *drops* first,
   then emit at most once per `broadcast`/`sendSnapshot` call for the batch — do not
   recurse into emitting drop-events for drops caused by delivering a drop-event.
4. Read `engine/internal/uihub/hub.go`'s `broadcast` and `sendSnapshot` functions and
   `conn.go`'s `enqueue`/`close`/`writeLoop` functions before writing code — match existing
   naming and comment style (this file has dense "why" comments; follow that convention for
   any non-obvious decision, e.g. why the self-send is avoided).

**Testing:** extend `engine/internal/uihub/conn_test.go` and `hub_test.go` following their
existing fake-based patterns (`fakeSocket` in conn_test.go, `fakeClient` in hub_test.go).
Add cases: (a) a write that blocks past the injected short `writeTimeout` results in the
conn closing; (b) a client dropped for overflow causes a `sys.events` frame with
`Kind: "ui-drop"` to reach a second, healthy, subscribed client. Keep existing tests
(`TestHubOverflowClosesClient`, any `server_test.go` overflow test) green — behavior for a
genuinely overflowing client (drop) must not change in this task, only the fact that it's
now also logged.

**Report:** implement with TDD, run `go test -race ./internal/uihub/...`, commit, and
report DONE/DONE_WITH_CONCERNS/BLOCKED/NEEDS_CONTEXT per the standard implementer contract.

## Task 2: Engine — recoverable outbound backpressure (outbox + coalescing)

**Goal:** the actual root-cause fix. Replace the conn's plain `out chan []byte` with a
topic-aware outbox so a slow client **coalesces** latest-wins frames (the newest value per
key supersedes any older queued value for that key) instead of overflowing and being
dropped, while event/ordered frames remain a lossless FIFO with only a large hard cap that,
if ever exceeded, still drops the connection (a genuinely pathological client) — reusing
Task 1's `ui-drop` sys.events instrumentation for that remaining drop path.

**Depends on Task 1** (reuses its drop-instrumentation helper) — do not start until Task 1
is committed and reviewed.

**Files:** `engine/internal/uihub/conn.go`, `engine/internal/uihub/hub.go`,
`engine/internal/uihub/coalesce.go`, `engine/internal/uihub/conn_test.go`,
`engine/internal/uihub/hub_test.go`.

**Design (implement this shape; deviate only if you find a correctness problem, and
explain why in your report):**

1. **Outbox data structure**, replacing `conn.out chan []byte`. Single mutex-guarded
   struct owned by the `conn`; the *only* goroutine that ever calls `ws.Write` remains
   `writeLoop` (single-writer discipline unchanged).
   ```go
   type qitem struct {
       b  []byte
       ck string // "" => lossless/ordered; non-empty => coalesce key
   }
   type outbox struct {
       mu     sync.Mutex
       q      []*qitem          // insertion-ordered; both lanes share one queue
       head   int               // consume index; compact q when drained to avoid a slice leak
       slots  map[string]*qitem // coalesce key -> its currently-queued item
       events int               // count of live items with ck == "" (lossless lane)
       cap    int               // hard cap on the lossless lane (from ServerConfig.OutBuf)
       notify chan struct{}     // buffered(1); wakes the writer
       closed bool
   }
   ```
   - `enqueue(b []byte, ck string) bool`: lock, if `closed` return false. If `ck != ""` and
     an item for that key is already queued (`slots[ck] != nil`), overwrite that item's `b`
     in place (same queue position — do not move it) and return true. Otherwise append a
     new item; for `ck != ""` register it in `slots`; for `ck == ""` (lossless) check
     `events >= cap` first and return false without enqueuing if so (this is the only
     remaining overflow/drop condition). Signal `notify` (non-blocking buffered send) on
     every successful enqueue.
   - `pop() ([]byte, bool)`: lock; if `head >= len(q)` the outbox is drained — compact
     (`q = q[:0]; head = 0`) and return `false` (do not let `q` grow unbounded across the
     connection's lifetime via naked `q[1:]` slicing). Otherwise return `q[head].b`, advance
     `head`, and account for removal: decrement `events` for a lossless item (`ck == ""`),
     or for a coalesced item, delete `slots[ck]` only if it still points at *this* item
     (coalescing overwrites `b` in place at the same queue position, so there is nothing
     else to skip — a superseded value is never a second entry in `q`, just an overwritten
     field on the one entry already there).
   - `close()`/`markClosed()`: set `closed = true` under the lock so any late `enqueue`
     after the conn is torn down returns false instead of leaking into the outbox forever.
2. **`client` interface** in `hub.go` changes to `enqueue(b []byte, ck string) bool`.
   Update every implementation and every test fake (`fakeClient` in `hub_test.go`, any
   other implementer of the interface) accordingly.
3. **`writeLoop`** in `conn.go`: replace the `select` over `c.out` with a loop that waits
   on the outbox's `notify` channel (plus `ctx.Done()`/`c.done`), then drains the outbox
   with `pop()` in an inner loop until empty, writing each frame with the Task 1
   write-timeout wrapper. On any write error, `c.close()` and return.
4. **`outboundCoalesceKey(s staged, snap bool) string`** — new function in `coalesce.go`,
   alongside the existing `classify`/`dedupOf`:
   - Returns `""` (lossless/ordered) when `snap` is true (**every** snapshot, of every
     topic, must always be lossless — never coalesced), and for deltas of these topics:
     `wsmsg.TopicTape`, `wsmsg.TopicExecOrders`, `wsmsg.TopicExecFills`,
     `wsmsg.TopicExecStatus`, `wsmsg.TopicSysEvents`, `wsmsg.TopicNews`,
     `wsmsg.TopicScannerHit`, `wsmsg.TopicConfig`, `wsmsg.TopicIndicator`.
   - Returns a non-empty coalesce key for deltas of: `wsmsg.TopicQuote`/`TopicBook`/
     `TopicBars` (reuse the existing `dedupOf(s)` function, prefixed to namespace it, e.g.
     `"d|" + dedupOf(s)`), `wsmsg.TopicExecAccount` (per venue, again via `dedupOf`),
     `wsmsg.TopicExecPositions` (one fixed key — it's a single full-replace slot),
     `wsmsg.TopicScannerRank` (key by `s.Key`, the session — e.g.
     `"d|scanner.rank|" + s.Key`), `wsmsg.TopicSysHealth` (one fixed key).
   - Look at `coalesce.go`'s existing `dedupOf` and `classify` to match style; add doc
     comments explaining *why* `scanner.rank`/`sys.health` are coalesceable here even
     though `classify` puts them in `classImmediate` for ingest-side batching purposes —
     the two are orthogonal (ingest coalescing controls *when* the Hub broadcasts; outbound
     coalescing controls what happens if a *specific slow client* can't keep up).
5. **Call sites in `hub.go`:**
   - `broadcast(s staged, snap bool)`: compute `ck := outboundCoalesceKey(s, snap)` once
     (the frame bytes are identical for every subscribed client) and pass it to every
     `c.enqueue(b, ck)` call.
   - `sendSnapshot`: always passes `""` (every snapshot frame is lossless per the design
     above).
   - `conn.go`'s `enqueueJSON` (used for ack/result/pong replies) always passes `""`.
   - The remaining drop path (lossless `events >= cap`, or Task 1's write-timeout) reuses
     Task 1's `ui-drop` sys.events emission unchanged.
6. Map `ServerConfig.OutBuf` (currently the channel buffer depth) to the outbox's `cap`
   field — same config knob, new meaning ("max in-flight lossless/ordered frames," not
   "max total frames").

**Testing (`go test -race`):** add a blockable fake socket (e.g. `gatedSocket`, wrapping
the existing `fakeSocket` in `conn_test.go` with a channel-based gate on `Write` so a test
can hold writes back and then release them) and cover:
1. **Coalesce-while-blocked:** hold the gate; `enqueue` 100 `md.quote` deltas for the same
   symbol (same coalesce key) interleaved with 100 lossless frames (e.g. simulated
   `md.tape`/`exec.fills`, `ck=""`); release the gate; assert the socket received exactly
   **one** quote frame (the last one enqueued) and all **100** lossless frames in their
   original order, and the conn was never closed.
2. **Event hard-cap overflow still drops:** construct an outbox/conn with a small `cap`;
   hold the gate; enqueue more lossless frames than `cap`; assert the enqueue past the cap
   returns false and the conn is closed (and, per Task 1's instrumentation, a `ui-drop`
   sys.events frame is emitted).
3. **Snapshot precedes later deltas:** hold the gate; enqueue a snapshot (`ck=""`) for a
   topic, then coalesceable deltas for the same topic/key; release; assert the snapshot
   appears before any delta in the drained write order.
4. At the hub level (`hub_test.go`), verify `outboundCoalesceKey` routes each topic to the
   expected lane (spot-check `md.quote`→coalesced, `md.tape`→lossless, `scanner.rank`→
   coalesced, `sys.events`→lossless) and that existing overflow tests
   (`TestHubOverflowClosesClient`, any equivalent in `server_test.go`) still pass with the
   new `enqueue` signature — update the fakes' signatures, not the tests' intent.

**Report:** implement with TDD, run `go test -race ./internal/uihub/...`, commit, and
report per the standard implementer contract. Flag any place where you deviated from the
data-structure sketch above and why.

## Task 3: UI — ReconnectOverlay grace period

**Goal:** make a brief reconnect invisible, so that even a residual sub-second WS blip
(which will now be much rarer after Task 2, but is never fully eliminated — e.g. an actual
engine restart) never flashes the whole app.

**Files:** `ui/src/chrome/ReconnectOverlay.tsx`. Check for an existing test file for this
component (e.g. `ReconnectOverlay.test.tsx`); if none exists, add one following this
project's existing React component test conventions (look at a sibling `chrome/` component
test for the pattern — testing-library + vitest).

**Requirements:**

1. `ReconnectOverlay` currently switches its dim/overlay UI immediately whenever `state !==
   "open"`. Change it to debounce: introduce internal state that only flips to "show the
   dim/overlay" after the connection has been continuously non-`"open"` for **600ms**. Use
   a `useEffect` keyed on `state` that starts a `setTimeout(600ms)` when `state` transitions
   away from `"open"`, and clears that timeout immediately (without ever showing the
   overlay) if `state` returns to `"open"` before it fires.
2. During the grace window (first 600ms of a disconnect), render children exactly as if
   `state === "open"` — full opacity, no overlay div. If the disconnect is still ongoing
   after 600ms, show the existing dimmed view + "connecting…"/"reconnecting…" text exactly
   as today.
3. Do not change the component's props/exported signature (`state`, `children`) — this is
   an internal behavior change only.
4. Add/extend a test asserting: (a) a `state` transition to `"reconnecting"` followed by a
   transition back to `"open"` within 600ms never renders the dimmed/overlay UI; (b) a
   `state` that stays `"reconnecting"` past 600ms does render it. Use fake timers
   (vitest's `vi.useFakeTimers()`) rather than real waits.

**Report:** implement with TDD, run the relevant UI test file(s) (e.g.
`npx vitest run src/chrome/ReconnectOverlay.test.tsx` — check `package.json` for the exact
test script/pattern this project uses), commit, and report per the standard implementer
contract.

## Task 4: Engine — raise OutboundQueue default

**Goal:** cheap additional headroom for snapshot bursts (e.g. many symbols' bar snapshots
delivered at once on a fresh subscribe), on top of Task 2's coalescing fix.

**Files:** `engine/internal/config/config.go`, `engine/internal/config/config_test.go`.

**Requirements:**

1. In `config.go`, raise the `UIHub.OutboundQueue` default from `1024` to `4096` (find the
   `DefaultConfig`-style constructor that currently sets `OutboundQueue: 1024, MDRateHz: 30,
   AccountRateHz: 4, PositionMs: 100, TapeSnapshot: 200`).
2. Update the doc comment on the `OutboundQueue` field (currently `"... overflow => drop +
   force re-snapshot"`) to reflect Task 2's new behavior: overflow of the lossless/ordered
   lane still drops the connection, but latest-wins topics (quotes, book, bars, account,
   positions, scanner rank, health) now coalesce instead of contributing to overflow.
3. Update the two existing test assertions that hardcode `1024`
   (`config_test.go`, currently asserting `cfg.UIHub.OutboundQueue == 1024` in two places)
   to expect `4096`.

**Testing:** `go test ./internal/config/...` (no race needed; this package has no
concurrency). This is a small, mechanical, low-risk change — implement directly, no TDD
ceremony required, but do add/update the assertions above and confirm the full package
still passes.

**Report:** implement, run `go test ./internal/config/...`, commit, and report per the
standard implementer contract.

## Verification (end-to-end, after all tasks land)

1. `cd engine && go test -race ./internal/uihub/... ./internal/config/...` — all green.
2. `cd ui && npm run build && npm test` (or the project's actual test script) — all green.
3. **Live confirmation (manual, outside this plan's automated scope):** run the engine +
   OpenD during market hours with a busy layout (chart + DOM + tape + movers panels open).
   Confirm `ui-drop` sys.events are now rare-to-absent during normal operation, and that a
   new mover arriving + the chime firing no longer visibly redraws the whole app. This step
   requires a live OpenD session and is left for Earl to run — do not attempt to simulate
   it in an automated test.
