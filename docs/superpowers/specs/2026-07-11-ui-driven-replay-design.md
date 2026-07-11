# Design: UI-driven replay (practice-mode entry) via engine self-restart

## Context

Today, replaying a recorded trading day requires launching the engine with
`-replay <YYYY-MM-DD> -speed <s>` on the command line. Earl wants to start (and
stop) replay **from the UI** — pick a recorded day + speed, click a button, and
the app enters practice mode: watch the day play back through chart/tape/DOM
**and** place simulated orders against it. Returning to live is one click. No CLI
argument.

**Constraint discovered during exploration:** replay is a **boot-time-only**
decision. `engine/cmd/etape/main.go:223` computes `live := *replayDay == ""` once
and threads it into four seams baked into the single-context goroutine graph —
feed source (`replay.Feed` vs OpenD), clock (`replay.Clock` vs `clock.System`),
brokers (`buildBrokers(..., !live)` forces every venue to `SimBroker` in replay),
and journaling/pollers. There is **no in-process pipeline rebuild**; the only
lifecycle control is cancelling the root ctx → ordered shutdown → exit.

**Chosen approach (Earl approved):** the engine **relaunches itself** into the
existing `-replay` path. This reuses 100% of the proven replay + SimBroker
machinery (already exercised by `-demo` and Playwright E2E), and matches the app's
existing "restart to apply" precedent for venue/credential edits
(`venueadmin` writes files + shows a restart banner rather than hot-reconfiguring).

**Scope (Earl approved):** watch + practice-trade; **start-only** transport (day +
speed chosen at start, plays forward to end; changing day/speed = another
restart). No pause / scrub / seek in v1.

Because replay already forces SimBroker on all venues and `markBridge` feeds the
replayed book/marks to the sim brokers, **order entry already works in replay** —
so the practice-trade goal comes for free once the engine is in replay mode. The
entire feature is about (a) driving the mode switch from the UI and (b) making the
live-vs-replay state unmistakable.

## Architecture

Three pieces:

1. **Engine — self-restart primitive.** Relaunch the current process with a
   rewritten argv, handing off the single-instance lock safely.
2. **Engine — control plane.** New WS commands `StartReplay{day,speed}` + `GoLive`,
   a new query `ListReplayDays`, and a **session-mode** value in the hub mirror so
   the UI always knows live vs replay (+ which day/speed).
3. **UI — launcher + banner.** A "Practice / Replay" dialog (day picker + speed) →
   `StartReplay`; a persistent, high-contrast **REPLAY** banner with "Return to
   live" → `GoLive`; the existing reconnect overlay covers the restart gap.

## Component 1 — Engine self-restart

New file `engine/cmd/etape/relaunch.go` (package main):

- `rewriteArgv(orig []string, mode) []string` — pure function: strip any existing
  `-replay`/`-speed` (and their values), then append `-replay <day> -speed <s>
  -replay-hold` for replay, or nothing for live. Preserve `-config`, `-dist`, etc.
  **`-replay-hold` is included** so the engine holds at end-of-day instead of
  self-terminating (`main.go:453` self-terminates without it) — the user must not
  be dumped when the journal drains.
- `spawnDetached(exe string, argv []string) error` — `os.Executable()` +
  `os.StartProcess` in a new process group, inheriting stdio/cwd, so the child
  survives the parent's exit.
- **Lock handoff:** parent holds `releaseLock` (`main.go:202/217`). Sequence on
  switch: spawn child → on spawn success call `releaseLock()` → cancel root ctx →
  ordered shutdown → exit. Add a **bounded retry** (~a few attempts over ~2s) to
  `singleinstance.Acquire` at boot so the child survives the brief handoff window,
  while a genuine double-launch still short-circuits to "open browser + exit"
  quickly.
- **Failure safety:** if `spawnDetached` errors, do **not** release the lock or
  cancel ctx — ack `blocked` and keep the current session alive.

Build-mode handling:
- Console / `embed_ui` release binary: `os.Executable()` is the real binary → works.
- Windows **tray** build: same binary; after spawn, cancelling ctx unwinds `boot`
  and `run_tray.go` calls `systray.Quit()`; the child re-runs systray fresh. Brief
  tray-icon flicker is acceptable (verify on a Windows machine).
- Dev `go run ./cmd/etape`: `os.Executable()` is the temp-compiled binary, present
  during the run → relaunch works. `run.sh`/`run.ps1` unaffected (still pass flags).

**Not changed:** the `-replay`/`-speed`/`-replay-hold`/`-demo` flags and the entire
`boot()` live/replay branching stay exactly as-is. The child is just a normal
`-replay` boot. This is the core risk-reducer.

## Component 2 — Engine control plane

**New `sessionCtl` interface** (in `commands.go`, alongside `execDoer` etc.),
satisfied by a small type in `cmd/etape` that owns the root `stop`, the original
`os.Args`, and the store handle:

```go
type sessionCtl interface {
    StartReplay(day string, speed float64) error // validates day vs JournalDays, spawns child, cancels ctx
    GoLive() error
}
```

- Inject through `uihub.New(...)` → `newCommands(...)` (mirror the existing
  `venueAdmin`/`venueTester` wiring at `api.go:76-99`, `commands.go:77`).
- New cases in the `commands.handle` switch (`commands.go:91`): `StartReplay`
  (validate speed ≥ 0; `sess.StartReplay` validates the day against
  `store.JournalDays()` internally and returns an error → `blocked` ack; otherwise
  ack `accepted` **then** relaunch), `GoLive`.
- The main-side impl validates `day` against `st.JournalDays()`, spawns the child,
  releases the lock, and cancels the root ctx.

**New query `ListReplayDays`** in the query handler (`newQueries`, `query.go`) →
returns `store.JournalDays()` (already implemented, `store/journal.go:182`). Add
`JournalDays() ([]string, error)` to the `Stores` interface (`api.go:20`);
`*store.Store` already satisfies it.

**Session-mode in the mirror** (`mirror.go` + a `sys.session` topic, or fold into
the existing `health` snapshot): seed `{mode:"live"|"replay", day?, speed?}` once at
boot from `live`/`*replayDay`/`*speed`. Add `Mode/ReplayDay/ReplaySpeed` fields to
`uihub.Config` and populate in `boot()` (it already has all three values). This is
the **safety-critical signal** — the client can always distinguish practice from
live. Add the topic to the `wsmsg` allow-list (`wsmsg.go:12-43`).

## Component 3 — UI

- **Wire helpers:** `sendQuery("ListReplayDays")` and `sendCommand("StartReplay",
  {day,speed})` / `sendCommand("GoLive")` (alongside `ui/src/chrome/exec/commands.ts`).
- **Session store:** subscribe to `sys.session`, expose `{mode, day, speed}` (small
  store like `ui/src/data/HealthStore.ts`).
- **Replay launcher dialog:** modal — "Practice: replay a recorded day." Day
  dropdown (from `ListReplayDays`, most-recent first) + speed selector
  (1× / 2× / 4× / Max) + "Start replay" → `StartReplay`. Entry point: a control in
  the app chrome near connection status. Reuse existing modal components. Empty day
  list → disable Start with a "no recorded days yet" hint.
- **REPLAY banner:** when `mode==="replay"`, a persistent, high-contrast banner
  (distinct from live) — e.g. `REPLAY — 2026-07-06 @ 4×` — with **"Return to
  live"** → `GoLive`. Consider tinting/badging the order-entry surfaces so a
  practice order can't be mistaken for live (ties into the live-order safety rules).
- **Restart gap:** after `StartReplay`/`GoLive` the WS drops; the existing
  `ReconnectOverlay` shows automatically; on reconnect the `sys.session` snapshot
  drives the banner. Optionally show a "Switching to replay…" hint on the overlay.

## Data flow (start replay)

1. User picks day+speed → `sendCommand("StartReplay",{day,speed})`.
2. Engine validates (speed ≥ 0; day ∈ `JournalDays()`), acks `accepted`.
3. Engine spawns detached child argv `… -replay <day> -speed <s> -replay-hold`,
   releases lock, cancels ctx → ordered shutdown → exit.
4. Child boots the standard replay path (feed=`replay.Feed`, clock=`replay.Clock`,
   all venues = SimBroker), serving UI+WS on the same port.
5. UI WS reconnects (overlay) → re-subscribes → `sys.session{mode:"replay",…}` →
   REPLAY banner.
6. User watches; sim orders fill against the replayed book via existing exec →
   SimBroker → `markBridge`.
7. "Return to live" → `GoLive` → child argv without `-replay` → live boot → banner
   clears.

## Edge cases / errors

- Spawn failure → ack `blocked`, keep live session (don't cancel ctx).
- Lock handoff → bounded child-side retry; real double-launch still exits fast.
- Unknown/empty day → validated in `StartReplay`; UI disables Start when list empty.
- End-of-day → `-replay-hold` keeps the engine serving; a later iteration can add an
  explicit "at end" flag on `sys.session`.
- Single-instance guarantee unchanged: exactly one engine owns the DB at a time.

## Testing / verification

- **Engine unit:** `rewriteArgv` table test (strip/append, preserve others);
  `StartReplay`/`GoLive` validation via the existing `commands_test` spy harness
  (mock `sessionCtl`, no real spawn).
- **Engine integration:** extend `uihubtest` — issue `StartReplay` over a test WS,
  assert ack + `sessionCtl` invoked; a separate opt-in test spawns a real child
  against a synthetic journal (`genjournal`/`demojournal`).
- **UI:** store test for `sys.session` → banner state; command-wrapper tests.
- **E2E (Playwright):** boot a real engine, seed a day via `genjournal`, open the
  dialog, Start replay → assert reconnect + REPLAY banner + a sim order fills →
  Return to live → banner clears. (E2E already boots the real engine in replay for
  fixtures — `ui/mock-engine/capture.ts`.)
- **Manual:** `./run.sh` (live or demo) → start/stop replay from the UI; confirm
  tray-build flicker acceptable on Windows.

## Out of scope (v1)

- Pause / scrub / seek / mid-replay speed change (start-only chosen).
- Live and replay running simultaneously.
- Persisting replay session across app restarts.
- SimBroker realism changes (already landed 2026-07-10).
