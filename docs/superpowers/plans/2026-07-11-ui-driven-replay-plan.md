# UI-Driven Replay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user start/stop practice-mode replay from the UI (pick a recorded day + speed, watch it play back and place simulated orders, one-click return to live) with no CLI flag.

**Architecture:** New WS commands `StartReplay{day,speed}` / `GoLive` build a rewritten argv and trigger the engine's **existing** restart machinery (`boot()` → `restart bool` → entrypoint calls `relaunch()`), landed on main just before this plan started as the `RestartEngine` feature (commit `5f61e08`). We extend that machinery with an optional argv override instead of building a second, competing restart pathway. A new static `sys.session` snapshot topic tells the UI which mode it's in, driving a mandatory REPLAY banner. A `ListReplayDays` query feeds a launcher dialog.

**Tech Stack:** Go (engine, `cmd/etape` + `internal/uihub` + `internal/store`), TypeScript/React/Vite (UI), tygo (Go→TS type gen), vitest + Playwright (tests).

## Global Constraints

- **Design spec:** `docs/superpowers/specs/2026-07-11-ui-driven-replay-design.md` (approved). Scope: watch + practice-trade; **start-only** transport (no pause/scrub).
- **Build on the landed `RestartEngine` mechanism (commit `5f61e08`), do not duplicate it.** `boot()` already returns `(code int, restart bool)`; entrypoints (`run_default.go`/`run_tray.go`) call `relaunch()` only *after* `boot()` fully returns — every deferred cleanup (`releaseLock`, `st.Close`, `httpSrv.Shutdown`) has already run, so there is **no lock-handoff race to solve** (no `-await-lock`, no retry loop, no spawn-before-shutdown). This plan only *extends* that machinery with an optional argv override.
- **Do NOT change** the `-replay`/`-speed`/`-replay-hold`/`-demo` flags or `boot()`'s live-vs-replay branching. The relaunched process is a normal `-replay` boot.
- **Safety:** the live-vs-replay signal + REPLAY banner are mandatory — practice must never be confusable with live (repo live-order safety rules).
- **Follow the `cd.restart` wiring precedent exactly:** `startReplay`/`goLive` closures are threaded through `uihub.New(...)`'s param list (a few call sites: `main.go`, `api_test.go`) and set post-construction on `*commands`, **not** threaded through `newCommands(...)` — this avoids touching the ~15 existing `newCommands(...)` call sites in `commands_test.go`, exactly as `cd.restart` did.
- **Ack-before-shutdown ordering matters:** the landed code uses `restartAckFlushDelay = 200 * time.Millisecond` (`time.AfterFunc`) so an "accepted" ack reaches the client before ctx cancellation starts tearing down the connection. `StartReplay`/`GoLive` must preserve this — but must validate (day exists, speed ≥ 0) *before* any delay, so a bad request blocks immediately rather than silently doing nothing 200ms later.
- **Shared checkout is live right now.** A concurrent session merged `5f61e08` to local `main` while this plan was being written. **Execute in a git worktree** (superpowers:using-git-worktrees), based off current local `main` (post-`5f61e08`), not `origin/main`. Re-run `git log --oneline -5` immediately before starting Task 1 to catch anything newer.
- **tygo contract is CI-gated:** any change to `internal/uihub/wsmsg` types requires `cd engine && make gen-ts` and committing the regenerated `ui/src/gen/wsmsg.ts` (`make gen-ts-check` fails otherwise).
- **Current command handler signature:** `func (cd *commands) handle(ctx, name string, args json.RawMessage, connID uint64, reply func(wsmsg.AckMsg)) (wsmsg.AckMsg, bool)`. `StartReplay`/`GoLive` return `(ack, false)` — never deferred.
- Commit after each task. Conventional-commit messages. No `Co-Authored-By` trailer.

---

## File Structure

**Engine (new):**
- `engine/cmd/etape/childargs.go` — pure `baseFlags`/`replayMode` types + `childArgs(base, mode) []string` builder (flags only, no binary path — `relaunch()` prepends `os.Executable()`).
- `engine/cmd/etape/childargs_test.go` — table tests.

**Engine (modified):**
- `engine/cmd/etape/relaunch_unix.go`, `relaunch_windows.go` — `relaunch()` gains an `argv []string` param (`nil` → reuse `os.Args`, matching today's `RestartEngine` behavior unchanged; non-nil → `exe` + the given flags).
- `engine/cmd/etape/main.go` — `boot()` returns a third value `nextArgs []string`; an `atomic.Pointer[[]string]` carries it from the `startReplay`/`goLive` closures to `boot()`'s final return; the two closures are built and passed into `uihub.New(...)`.
- `engine/cmd/etape/run_default.go`, `run_tray.go` — capture `nextArgs` from `boot()` and pass it to `relaunch(nextArgs)`.
- `engine/internal/uihub/commands.go` — `startReplay`/`goLive` fields on `commands` + `StartReplay`/`GoLive` switch cases.
- `engine/internal/uihub/commands_test.go` — tests mirroring `TestCommandsRestartEngineBlockedWithNoHandler`/`AcksThenTriggers`.
- `engine/internal/uihub/api.go` — `New(...)` gains `startReplay`/`goLive` params (set on `cmd`, like `requestRestart`); `Config` mode fields; seed mirror session; `Stores` gains `JournalDays`; `newQueries` gains journal dep.
- `engine/internal/uihub/api_test.go` — pad the `New(...)` call site with two more `nil`s.
- `engine/internal/uihub/wsmsg/wsmsg.go` — `TopicSysSession` const + `AllTopics`.
- `engine/internal/uihub/wsmsg/payloads.go` — `StartReplayArgs`, `GoLiveArgs`, `SessionSnapshot`.
- `engine/internal/uihub/mirror.go` — `session` field + `snapshotFrames` case.
- `engine/internal/uihub/query.go` — `journalQuerier` + `ListReplayDays` case.
- `engine/internal/uihub/session_test.go` (new) — session snapshot test.
- `engine/tygo.yaml` — add `"sys.session"` to the `Topic` frontmatter union.

**UI (new):**
- `ui/src/data/SessionStore.ts` + `SessionStore.test.ts`
- `ui/src/chrome/ReplayBanner.tsx`
- `ui/src/chrome/ReplayLauncherModal.tsx`
- `ui/src/chrome/exec/useReplayCommands.ts`
- `ui/e2e/replay-launcher.spec.ts`

**UI (modified):**
- `ui/src/gen/wsmsg.ts` — regenerated (Topic union + `SessionSnapshot`).
- `ui/src/data/registry.ts` — register + route `SessionStore`.
- `ui/src/chrome/panels/registry.tsx` — add `"sys.session"` to the Connection panel's always-on `topics` list (same mechanism `sys.health`/`sys.events` use to be always subscribed).
- `ui/src/chrome/AppShell.tsx` — mount banner + launcher modal + state; pass `engineState` down (already threaded to `SettingsModal` since `5f61e08`).
- `ui/src/chrome/TopBar.tsx` — "Practice" launcher button.
- `ui/src/chrome/panels/OrderTicketPanel.tsx` — PRACTICE badge when replay.

---

## Task 1: Engine — `childArgs` pure builder

**Files:**
- Create: `engine/cmd/etape/childargs.go`
- Test: `engine/cmd/etape/childargs_test.go`

**Interfaces produced (used by Task 4):**
- `type baseFlags struct { ConfigPath, DistDir, LogPath string }`
- `type replayMode struct { Live bool; Day string; Speed float64 }`
- `func childArgs(base baseFlags, mode replayMode) []string` — flags only, always includes `-no-open` (the user already has the tab open; a relaunch must not pop a new one) and, for replay, `-replay-hold`.

- [ ] **Step 1: Write the failing test** — `engine/cmd/etape/childargs_test.go`

```go
package main

import (
	"reflect"
	"testing"
)

func TestChildArgsReplay(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml", DistDir: "ui/dist"}, replayMode{Live: false, Day: "2026-07-06", Speed: 4})
	want := []string{"-config", "/c.toml", "-dist", "ui/dist", "-no-open", "-replay", "2026-07-06", "-speed", "4", "-replay-hold"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs replay:\n got=%v\nwant=%v", got, want)
	}
}

func TestChildArgsLiveOmitsReplayFlags(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml"}, replayMode{Live: true})
	want := []string{"-config", "/c.toml", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs live:\n got=%v\nwant=%v", got, want)
	}
}

func TestChildArgsPreservesLogPath(t *testing.T) {
	got := childArgs(baseFlags{ConfigPath: "/c.toml", LogPath: "/var/log/etape.log"}, replayMode{Live: true})
	want := []string{"-config", "/c.toml", "-log", "/var/log/etape.log", "-no-open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("childArgs log:\n got=%v\nwant=%v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./cmd/etape/ -run ChildArgs -v`
Expected: FAIL — `undefined: childArgs`.

- [ ] **Step 3: Implement `engine/cmd/etape/childargs.go`**

```go
package main

import "strconv"

// baseFlags are the launch flags a relaunch must preserve across a mode switch.
type baseFlags struct {
	ConfigPath string
	DistDir    string
	LogPath    string
}

// replayMode selects what the relaunched process boots into.
type replayMode struct {
	Live  bool
	Day   string
	Speed float64
}

// childArgs builds the flag list for a self-triggered relaunch into a
// different mode (see relaunch_unix.go/relaunch_windows.go, which prepend the
// executable path). It rebuilds from known flag values rather than editing
// os.Args, because -demo mutates flag values in place at boot. -no-open is
// always included: the user is mid-session in an open browser tab (they just
// clicked a UI control), so a relaunch must never pop a new one.
func childArgs(base baseFlags, mode replayMode) []string {
	argv := []string{"-config", base.ConfigPath}
	if base.DistDir != "" {
		argv = append(argv, "-dist", base.DistDir)
	}
	if base.LogPath != "" {
		argv = append(argv, "-log", base.LogPath)
	}
	argv = append(argv, "-no-open")
	if !mode.Live {
		argv = append(argv, "-replay", mode.Day,
			"-speed", strconv.FormatFloat(mode.Speed, 'f', -1, 64), "-replay-hold")
	}
	return argv
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test ./cmd/etape/ -run ChildArgs -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add engine/cmd/etape/childargs.go engine/cmd/etape/childargs_test.go
git commit -m "feat(engine): add childArgs builder for mode-switch relaunches"
```

---

## Task 2: Engine — `relaunch()` argv override + `boot()` nextArgs plumbing

**Files:**
- Modify: `engine/cmd/etape/relaunch_unix.go`, `engine/cmd/etape/relaunch_windows.go`, `engine/cmd/etape/main.go`, `engine/cmd/etape/run_default.go`, `engine/cmd/etape/run_tray.go`

**Interfaces produced (consumed by Task 4):** `relaunch(argv []string) error`; `boot(...) (code int, restart bool, nextArgs []string)`.

**No new test file** — `relaunch_unix.go`/`relaunch_windows.go` have no existing tests (they call `syscall.Exec`/`os.StartProcess`, which replace/spawn a real process; the landed `RestartEngine` commit didn't unit-test them either). Correctness is covered by `go build`/`go vet` on both platforms and the Task 11 manual round-trip.

- [ ] **Step 1: Extend `relaunch()` on Unix** — `engine/cmd/etape/relaunch_unix.go`. Current signature is `func relaunch() error { ... syscall.Exec(exe, os.Args, os.Environ()) }`. Change to:

```go
// relaunch replaces the current process image in place (same PID). argv is
// the flag list to boot with; nil means "reuse os.Args unchanged" (the
// existing RestartEngine case: same flags, changed config file on disk).
// Non-nil (a mode-switch relaunch) rebuilds argv as [exe, argv...] so the
// child sees a clean, correct os.Args regardless of how this process was
// originally invoked.
func relaunch(argv []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	next := os.Args
	if argv != nil {
		next = append([]string{exe}, argv...)
	}
	return syscall.Exec(exe, next, os.Environ())
}
```

- [ ] **Step 2: Extend `relaunch()` on Windows** — `engine/cmd/etape/relaunch_windows.go`. Same shape, using `os.StartProcess`:

```go
func relaunch(argv []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	next := os.Args
	if argv != nil {
		next = append([]string{exe}, argv...)
	}
	proc, err := os.StartProcess(exe, next, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{nil, nil, nil},
	})
	if err != nil {
		return err
	}
	return proc.Release()
}
```

- [ ] **Step 3: Extend `boot()`'s return signature and add the nextArgs handoff** — `engine/cmd/etape/main.go`. Change the signature and every existing `return N, false` to `return N, false, nil` (the existing `restartRequested.Load()` final return becomes the only one that can carry real args):

```go
func boot(ctx context.Context, onListening func(addr string)) (code int, restart bool, nextArgs []string) {
```

Near the existing `restartRequested`/`requestRestart` declaration, add:

```go
	// nextArgs carries a mode-switch relaunch's flag list from the
	// startReplay/goLive closures (built below, passed into uihub.New) to
	// boot's final return. atomic.Pointer because it's written from the
	// command-dispatch goroutine (via time.AfterFunc, same as
	// requestRestart) and read here after <-ctx.Done() on the boot
	// goroutine — nil means "plain RestartEngine: reuse os.Args".
	var nextArgsPtr atomic.Pointer[[]string]
```

Update every `return 1` / `return 0` in `boot()` to append `, nil` as the third value (mechanical; there are ~9 sites, all already updated once from `int` to `int, bool` by the landed `RestartEngine` commit — this pass just adds one more `, nil`). Replace the final `return 0, restartRequested.Load()` with:

```go
	var na []string
	if p := nextArgsPtr.Load(); p != nil {
		na = *p
	}
	return 0, restartRequested.Load(), na
```

- [ ] **Step 4: Update the entrypoints.** `engine/cmd/etape/run_default.go`:

```go
	code, restart, nextArgs := boot(ctx, nil)
	if restart {
		if err := relaunch(nextArgs); err != nil {
			slog.Default().Error("relaunch failed", "err", err)
		}
	}
	os.Exit(code)
```

`engine/cmd/etape/run_tray.go` (inside the existing boot goroutine):

```go
		code, restart, nextArgs := boot(ctx, captureAddr)
		if code != 0 {
			slog.Default().Error("boot failed", "code", code)
		}
		if restart {
			if err := relaunch(nextArgs); err != nil {
				slog.Default().Error("relaunch failed", "err", err)
			}
		}
		systray.Quit()
```

- [ ] **Step 5: Build + run the existing suite (this must be a no-op behavior change for `RestartEngine`)**

Run: `cd engine && go build ./... && go vet ./... && go test ./cmd/etape/ ./internal/uihub/ -count=1`
Expected: build OK, PASS. `TestCommandsRestartEngineAcksThenTriggers` still passes unchanged (it doesn't touch `boot`/`relaunch` directly).

- [ ] **Step 6: Commit**

```bash
git add engine/cmd/etape/relaunch_unix.go engine/cmd/etape/relaunch_windows.go engine/cmd/etape/main.go engine/cmd/etape/run_default.go engine/cmd/etape/run_tray.go
git commit -m "feat(engine): thread an optional argv override through relaunch"
```

---

## Task 3: Engine — `StartReplay`/`GoLive` command dispatch

**Files:**
- Modify: `engine/internal/uihub/commands.go`, `engine/internal/uihub/wsmsg/payloads.go`
- Test: `engine/internal/uihub/commands_test.go`

**Interfaces produced:** `commands.startReplay func(day string, speed float64) error`, `commands.goLive func() error` (fields, set post-construction — see Task 4); `StartReplayArgs{Day string; Speed float64}`, `GoLiveArgs struct{}`.

- [ ] **Step 1: Add args structs** — `engine/internal/uihub/wsmsg/payloads.go`:

```go
type StartReplayArgs struct {
	Day   string  `json:"day"`
	Speed float64 `json:"speed"`
}

// GoLiveArgs is intentionally empty (kept as a named type for tygo stability).
type GoLiveArgs struct{}
```

- [ ] **Step 2: Write the failing tests** — append to `commands_test.go`, mirroring `TestCommandsRestartEngineBlockedWithNoHandler`/`AcksThenTriggers`:

```go
func TestCommandsStartReplayBlockedWithNoHandler(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, deferred := cd.handle(context.Background(), "StartReplay", mustJSON(t, wsmsg.StartReplayArgs{Day: "2026-07-06"}), 0, func(wsmsg.AckMsg) {})
	if deferred {
		t.Fatal("StartReplay must not be deferred")
	}
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("StartReplay with no handler: status = %q, want blocked", ack.Status)
	}
}

func TestCommandsStartReplayRejectsNegativeSpeed(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.startReplay = func(string, float64) error { t.Fatal("handler must not run for invalid args"); return nil }
	ack, _ := cd.handle(context.Background(), "StartReplay", mustJSON(t, wsmsg.StartReplayArgs{Day: "x", Speed: -1}), 0, func(wsmsg.AckMsg) {})
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("want blocked for negative speed, got %+v", ack)
	}
}

func TestCommandsStartReplayDispatchesAndAcks(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	var gotDay string
	var gotSpeed float64
	cd.startReplay = func(day string, speed float64) error { gotDay, gotSpeed = day, speed; return nil }
	ack, deferred := cd.handle(context.Background(), "StartReplay", mustJSON(t, wsmsg.StartReplayArgs{Day: "2026-07-06", Speed: 4}), 0, func(wsmsg.AckMsg) {
		t.Fatal("StartReplay's ack must be synchronous, not delivered via reply")
	})
	if deferred || ack.Status != wsmsg.AckAccepted {
		t.Fatalf("want accepted/non-deferred, got %+v deferred=%v", ack, deferred)
	}
	if gotDay != "2026-07-06" || gotSpeed != 4 {
		t.Fatalf("handler got day=%q speed=%v", gotDay, gotSpeed)
	}
}

func TestCommandsStartReplayBlockedOnHandlerError(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.startReplay = func(string, float64) error { return errors.New(`no recorded day "x"`) }
	ack, _ := cd.handle(context.Background(), "StartReplay", mustJSON(t, wsmsg.StartReplayArgs{Day: "x"}), 0, func(wsmsg.AckMsg) {})
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("want blocked, got %+v", ack)
	}
}

func TestCommandsGoLiveDispatchesAndAcks(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	hit := false
	cd.goLive = func() error { hit = true; return nil }
	ack, _ := cd.handle(context.Background(), "GoLive", mustJSON(t, wsmsg.GoLiveArgs{}), 0, func(wsmsg.AckMsg) {})
	if !hit || ack.Status != wsmsg.AckAccepted {
		t.Fatalf("GoLive not dispatched: hit=%v ack=%+v", hit, ack)
	}
}
```

(Add `"errors"` to the test file's imports if not already present — it is, per the existing `RestartEngine` tests' neighbors; verify with a quick grep before assuming.)

- [ ] **Step 3: Run to verify failure**

Run: `cd engine && go test ./internal/uihub/ -run 'StartReplay|GoLive' -v`
Expected: FAIL — `commands` has no field `startReplay`/`goLive`.

- [ ] **Step 4: Implement** — `engine/internal/uihub/commands.go`. Add fields next to `restart` (following its exact comment style):

```go
	// startReplay/goLive are set post-construction by uihub.New, same pattern
	// as restart above (see api.go) — kept out of newCommands' param list so
	// the many existing newCommands(...) call sites in commands_test.go don't
	// need updating. Unlike restart, they carry arguments and can fail
	// validation (bad day, negative speed), so each call is expected to
	// validate synchronously and return an error for a blocked ack *before*
	// scheduling any delayed side effect — see the closures built in main.go.
	startReplay func(day string, speed float64) error
	goLive      func() error
```

Add cases before `default:`:

```go
	case "StartReplay":
		var a wsmsg.StartReplayArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		if a.Speed < 0 {
			return blocked("speed must be >= 0"), false
		}
		if cd.startReplay == nil {
			return blocked("replay switching not supported"), false
		}
		if err := cd.startReplay(a.Day, a.Speed); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted}, false
	case "GoLive":
		if cd.goLive == nil {
			return blocked("replay switching not supported"), false
		}
		if err := cd.goLive(); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted}, false
```

- [ ] **Step 5: Run to verify pass**

Run: `cd engine && go test ./internal/uihub/ -run 'StartReplay|GoLive|RestartEngine' -v`
Expected: PASS (new tests + the existing `RestartEngine` tests still green — confirms the shared `commands` struct wiring didn't regress it).

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/commands.go engine/internal/uihub/commands_test.go engine/internal/uihub/wsmsg/payloads.go
git commit -m "feat(engine): add StartReplay/GoLive command dispatch"
```

---

## Task 4: Engine — wire closures through `uihub.New` and `main.go`

**Files:**
- Modify: `engine/internal/uihub/api.go`, `engine/internal/uihub/api_test.go`, `engine/cmd/etape/main.go`

**Interfaces:** Consumes `childArgs` (Task 1), `nextArgsPtr`/`requestRestart` (Task 2), `commands.startReplay`/`goLive` (Task 3). Produces the live `StartReplay`/`GoLive` behavior.

- [ ] **Step 1: Extend `uihub.New`** — `engine/internal/uihub/api.go`. Current (post-`5f61e08`) signature:

```go
func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators, va venueAdmin, vt venueTester, requestRestart func()) (*Hub, *Server) {
```

Add two params and set them on `cmd` next to the existing `cmd.restart = requestRestart`:

```go
func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators, va venueAdmin, vt venueTester, requestRestart func(), startReplay func(day string, speed float64) error, goLive func() error) (*Hub, *Server) {
	...
	cmd := newCommands(ex, st, ind, h, va, h.feed, vt)
	cmd.restart = requestRestart
	cmd.startReplay = startReplay
	cmd.goLive = goLive
	...
}
```

- [ ] **Step 2: Update the test call site** — `engine/internal/uihub/api_test.go:40`. Current call ends:

```go
	}, apiExec{}, apiStores{}, apiInd{}, nil, nil, nil)
```

(the trailing three `nil`s are `va, vt, requestRestart`). Append two more for `startReplay, goLive`:

```go
	}, apiExec{}, apiStores{}, apiInd{}, nil, nil, nil, nil, nil)
```

- [ ] **Step 3: Build the closures in `main.go`** and pass them into the `uihub.New(...)` call. Locate the existing `requestRestart` declaration (added by `5f61e08`, near `ctx, stop := context.WithCancel(ctx)`) and the `nextArgsPtr` from Task 2, then add:

```go
	base := baseFlags{ConfigPath: *cfgPath, DistDir: *dist, LogPath: *logPath}
	startReplay := func(day string, speed float64) error {
		if *demo {
			return fmt.Errorf("replay switching is unavailable in demo mode")
		}
		days, err := st.JournalDays()
		if err != nil {
			return fmt.Errorf("list recorded days: %w", err)
		}
		found := false
		for _, d := range days {
			if d == day {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("no recorded day %q", day)
		}
		argv := childArgs(base, replayMode{Live: false, Day: day, Speed: speed})
		time.AfterFunc(relaunchAckFlushDelay, func() {
			nextArgsPtr.Store(&argv)
			requestRestart()
		})
		return nil
	}
	goLive := func() error {
		if *demo {
			return fmt.Errorf("replay switching is unavailable in demo mode")
		}
		argv := childArgs(base, replayMode{Live: true})
		time.AfterFunc(relaunchAckFlushDelay, func() {
			nextArgsPtr.Store(&argv)
			requestRestart()
		})
		return nil
	}
```

Verified: `restartAckFlushDelay` is unexported in `engine/internal/uihub/commands.go:92` — `main.go` (`package main`) cannot reference it. Define a local equivalent in `main.go`, matching its value and comment intent exactly:

```go
// relaunchAckFlushDelay mirrors uihub's own restartAckFlushDelay (package-
// private, so not importable from here): give the "accepted" ack time to
// reach the client before ctx cancellation starts tearing down the connection.
const relaunchAckFlushDelay = 200 * time.Millisecond
```

The call site is `engine/cmd/etape/main.go:307`, `hub, srv := uihub.New(uihubClk, uihub.Config{...}, execCore, st, core, venueAdm, venueProbe, requestRestart)` (per `5f61e08`). Append the two new closures:

```go
	hub, srv := uihub.New(uihubClk, uihub.Config{
		...
	}, execCore, st, core, venueAdm, venueProbe, requestRestart, startReplay, goLive)
```

- [ ] **Step 4: Build + test**

Run: `cd engine && go build ./... && go vet ./... && go test ./... -count=1`
Expected: PASS across the whole engine module (this is the first point where Tasks 1-4 combine end-to-end).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/api.go engine/internal/uihub/api_test.go engine/cmd/etape/main.go
git commit -m "feat(engine): wire StartReplay/GoLive to the relaunch machinery"
```

---

## Task 5: Engine — `sys.session` snapshot topic

**Files:**
- Modify: `engine/internal/uihub/wsmsg/wsmsg.go`, `payloads.go`, `mirror.go`, `api.go`, `engine/cmd/etape/main.go`, `engine/tygo.yaml`
- Test: new `engine/internal/uihub/session_test.go`

**Interfaces produced:** `SessionSnapshot{ Mode, Day, Speed }`; topic `"sys.session"`; `uihub.Config.{Mode,ReplayDay,ReplaySpeed}`.

- [ ] **Step 1: Add the topic** — `wsmsg.go`: add `TopicSysSession Topic = "sys.session"` in the `sys.*` const group and `TopicSysSession: true` to the `AllTopics` map.

- [ ] **Step 2: Add the payload** — `payloads.go`:

```go
// SessionSnapshot is the static sys.session topic: which mode the engine
// booted in. Mode is "live" or "replay"; Day/Speed populated only in replay.
type SessionSnapshot struct {
	Mode  string  `json:"mode"`
	Day   string  `json:"day,omitempty"`
	Speed float64 `json:"speed,omitempty"`
}
```

- [ ] **Step 3: Write the failing test** — `engine/internal/uihub/session_test.go`

```go
package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestSnapshotFramesSession(t *testing.T) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10, 10)
	m.session = wsmsg.SessionSnapshot{Mode: "replay", Day: "2026-07-06", Speed: 4}
	frames := m.snapshotFrames(wsmsg.TopicSysSession)
	if len(frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(frames))
	}
	got, ok := frames[0].Payload.(wsmsg.SessionSnapshot)
	if !ok || got.Mode != "replay" || got.Day != "2026-07-06" || got.Speed != 4 {
		t.Fatalf("bad session frame: %+v", frames[0].Payload)
	}
}
```

Run: `cd engine && go test ./internal/uihub/ -run SnapshotFramesSession -v` → FAIL (`session` field / topic case undefined).

- [ ] **Step 4: Implement.** `mirror.go`: add `session wsmsg.SessionSnapshot` under the `// system` field block; add to `snapshotFrames`:

```go
	case wsmsg.TopicSysSession:
		out = append(out, staged{Topic: topic, Payload: m.session})
```

`api.go`: add fields to `Config`:

```go
	Mode        string  // "live" | "replay"
	ReplayDay   string
	ReplaySpeed float64
```

In `New`, after `m := newMirror(...)` and before `NewHub`:

```go
	m.session = wsmsg.SessionSnapshot{Mode: cfg.Mode, Day: cfg.ReplayDay, Speed: cfg.ReplaySpeed}
```

`main.go`: in the `uihub.Config{...}` literal set the mode fields from the already-computed `live`/`*replayDay`/`*speed`:

```go
		Mode: func() string { if live { return "live" }; return "replay" }(),
		ReplayDay: *replayDay, ReplaySpeed: *speed,
```

- [ ] **Step 5: Run to verify pass**

Run: `cd engine && go test ./internal/uihub/ -run SnapshotFramesSession -v`
Expected: PASS.

- [ ] **Step 6: Regenerate the TS contract.** `engine/tygo.yaml`: add `| "sys.session"` to the `Topic` union in the `frontmatter` block. Then:

Run: `cd engine && make gen-ts && make gen-ts-check`
Expected: `ui/src/gen/wsmsg.ts` regenerated with `"sys.session"` in the union and a `SessionSnapshot` interface; `gen-ts-check` passes.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/uihub/wsmsg/wsmsg.go engine/internal/uihub/wsmsg/payloads.go engine/internal/uihub/mirror.go engine/internal/uihub/api.go engine/internal/uihub/session_test.go engine/cmd/etape/main.go engine/tygo.yaml ui/src/gen/wsmsg.ts
git commit -m "feat(engine): add sys.session snapshot topic (live/replay mode)"
```

---

## Task 6: Engine — `ListReplayDays` query

**Files:**
- Modify: `engine/internal/uihub/query.go`, `engine/internal/uihub/api.go`
- Test: `engine/internal/uihub/query_test.go` (create if absent)

**Interfaces produced:** query `"ListReplayDays"` → `[]string` (recorded days). `Stores` gains `JournalDays() ([]string, error)`.

- [ ] **Step 1: Write the failing test** — `engine/internal/uihub/query_test.go`

```go
package uihub

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

type spyJournal struct{ days []string }

func (s *spyJournal) JournalDays() ([]string, error) { return s.days, nil }

func TestListReplayDays(t *testing.T) {
	q := newQueries(nil, &spyJournal{days: []string{"2026-07-06", "2026-07-05"}}, clock.System{})
	got := q.handle("ListReplayDays", json.RawMessage(`{}`))
	if !reflect.DeepEqual(got, []string{"2026-07-06", "2026-07-05"}) {
		t.Fatalf("got %#v", got)
	}
}
```

Run: `cd engine && go test ./internal/uihub/ -run ListReplayDays -v` → FAIL.

- [ ] **Step 2: Implement.** `query.go`: add interface + field + constructor param + case:

```go
type journalQuerier interface {
	JournalDays() ([]string, error)
}

type queries struct {
	fills   fillsQuerier
	journal journalQuerier
	clk     clock.Clock
}

func newQueries(f fillsQuerier, j journalQuerier, clk clock.Clock) *queries {
	return &queries{fills: f, journal: j, clk: clk}
}
```

In `handle`'s switch (before `default`):

```go
	case "ListReplayDays":
		days, err := q.journal.JournalDays()
		if err != nil {
			return []string{}
		}
		return days
```

`api.go`: add `JournalDays() ([]string, error)` to the `Stores` interface, and change the constructor call to `qry := newQueries(st, st, clk)`. (`*store.Store` already implements `JournalDays` — see `store/journal.go:182`.)

- [ ] **Step 3: Run to verify pass + build**

Run: `cd engine && go build ./... && go test ./internal/uihub/ -run ListReplayDays -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add engine/internal/uihub/query.go engine/internal/uihub/api.go engine/internal/uihub/query_test.go
git commit -m "feat(engine): add ListReplayDays query"
```

---

## Task 7: UI — SessionStore + registry wiring + subscription

**Files:**
- Create: `ui/src/data/SessionStore.ts`, `ui/src/data/SessionStore.test.ts`
- Modify: `ui/src/data/registry.ts`, `ui/src/chrome/panels/registry.tsx`

**Interfaces produced:** `SessionStore` with `getSnapshot(): SessionState` where `SessionState = { mode: "live" | "replay"; day?: string; speed?: number }`.

- [ ] **Step 1: Write the failing test** — `ui/src/data/SessionStore.test.ts`

```ts
import { describe, it, expect, vi } from "vitest";
import { SessionStore } from "./SessionStore";
import type { SnapshotMsg } from "../wire/contract";

const snap = (mode: "live" | "replay", day?: string, speed?: number): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.session", payload: { mode, day, speed },
});

describe("SessionStore", () => {
  it("defaults to live and applies a replay snapshot, notifying subscribers", () => {
    const s = new SessionStore();
    expect(s.getSnapshot().mode).toBe("live");
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(snap("replay", "2026-07-06", 4));
    expect(s.getSnapshot()).toEqual({ mode: "replay", day: "2026-07-06", speed: 4 });
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
```

Run: `cd ui && npx vitest run src/data/SessionStore.test.ts` → FAIL.

- [ ] **Step 2: Implement** — `ui/src/data/SessionStore.ts`

```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, SessionSnapshot } from "../wire/contract";

export type SessionState = SessionSnapshot; // { mode: "live" | "replay"; day?: string; speed?: number }

export class SessionStore extends ReactStore<SessionState> {
  constructor() {
    super({ mode: "live" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.session") return;
    this.set(m.payload as SessionState); // static topic: snapshot & delta are both full replaces
  }
}
```

(If `SessionSnapshot.mode` generates as `string`, cast/annotate `SessionState = { mode: "live" | "replay"; day?: string; speed?: number }` explicitly.)

- [ ] **Step 3: Wire into the registry** — `ui/src/data/registry.ts`: add `session: new SessionStore()` to `makeStores()` (and the `Stores` type), and in `routeToStore` add `case "sys.session": stores.session.apply(m); return;`.

- [ ] **Step 4: Subscribe the topic globally.** `App.tsx` subscribes the union of every catalog panel's declared `topics` up front (`ui/src/App.tsx:139-143`), the same way `sys.health`/`sys.events` are always-on via the Connection panel's entry at `ui/src/chrome/panels/registry.tsx:80` (`topics: ["sys.health", "sys.events"]`). Add `"sys.session"` to that same array so it's always subscribed regardless of which panels are mounted — no `App.tsx` change needed:

```ts
    topics: ["sys.health", "sys.events", "sys.session"],
```

- [ ] **Step 5: Run tests**

Run: `cd ui && npx vitest run src/data/SessionStore.test.ts && npx tsc --noEmit`
Expected: PASS; typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add ui/src/data/SessionStore.ts ui/src/data/SessionStore.test.ts ui/src/data/registry.ts ui/src/chrome/panels/registry.tsx
git commit -m "feat(ui): SessionStore tracking live/replay mode from sys.session"
```

---

## Task 8: UI — REPLAY banner + Return-to-live

**Files:**
- Create: `ui/src/chrome/ReplayBanner.tsx`, `ui/src/chrome/exec/useReplayCommands.ts`
- Modify: `ui/src/chrome/AppShell.tsx`

**Interfaces produced (consumed by Task 9):** `useReplayCommands(commands) => { listDays(): Promise<string[]>; start(day, speed): Promise<AckMsg>; goLive(): Promise<AckMsg> }`.

**Reuse note:** `VenuesSection.tsx`'s `restartEngine`/`sawDropRef` pattern (landed in `5f61e08`) is the reference for surfacing a restart-in-flight state correctly — it awaits the ack, then watches `engineState` for a genuine `open → non-open → open` cycle (not just "still open," since the ack arrives ~200ms before the socket actually drops) before treating the switch as complete. `AppShell.tsx` already threads `engineState` down to `SettingsModal` since that commit; thread it to `ReplayBanner` the same way.

- [ ] **Step 1: Command wrappers** — `ui/src/chrome/exec/useReplayCommands.ts`

```ts
import { useMemo } from "react";
import type { AckMsg } from "../../wire/contract";

export interface ReplayCommandAdapter {
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  sendQuery(name: string, args: unknown): Promise<unknown>;
}

export function useReplayCommands(cmd: ReplayCommandAdapter) {
  return useMemo(() => ({
    listDays: async (): Promise<string[]> => ((await cmd.sendQuery("ListReplayDays", {})) as string[]) ?? [],
    start: (day: string, speed: number): Promise<AckMsg> => cmd.sendCommand("StartReplay", { day, speed }),
    goLive: (): Promise<AckMsg> => cmd.sendCommand("GoLive", {}),
  }), [cmd]);
}
```

- [ ] **Step 2: Banner component** — `ui/src/chrome/ReplayBanner.tsx` (model on `FeedStatusBanner.tsx` for layout; model the drop-then-reconnect detection on `VenuesSection.tsx`'s `sawDropRef` effect):

```tsx
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { SessionStore } from "../data/SessionStore";
import type { ConnState } from "../wire/WsClient";
import { useTheme } from "./ThemeProvider";

export function ReplayBanner({ session, engineState, onGoLive }: {
  session: SessionStore; engineState: ConnState | undefined; onGoLive: () => Promise<void>;
}): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore((cb) => session.subscribe(cb), () => session.getSnapshot());
  const [returning, setReturning] = useState(false);
  const sawDropRef = useRef(false);

  useEffect(() => {
    if (!returning) { sawDropRef.current = false; return; }
    if (engineState !== "open") { sawDropRef.current = true; return; }
    if (sawDropRef.current) setReturning(false); // banner clears itself once sys.session reports "live" again
  }, [engineState, returning]);

  if (s.mode !== "replay") return null;
  const speed = s.speed && s.speed > 0 ? `${s.speed}×` : "max";
  return (
    <div data-testid="replay-banner" style={{
      display: "flex", alignItems: "center", justifyContent: "center", gap: 12,
      padding: "4px 12px", background: palette.warn, color: "#fff", fontWeight: 600,
    }}>
      <span>REPLAY — {s.day} @ {speed} · practice orders only</span>
      <button data-testid="return-to-live" disabled={returning} onClick={() => { setReturning(true); void onGoLive(); }}
        style={{ padding: "2px 10px", borderRadius: 4, border: "1px solid #fff", background: "transparent", color: "#fff", cursor: "pointer" }}>
        {returning ? "Returning to live…" : "Return to live"}
      </button>
    </div>
  );
}
```

(`palette.warn` is `#9A6A1B` light / `#C79A4B` dark — verified in `ui/src/render/palette.ts`; `useTheme` is exported from `ui/src/chrome/ThemeProvider.tsx`.) Note this banner un-mounts itself naturally once `sys.session` reports `mode: "live"` after reconnect — `sawDropRef`/`returning` only smooth over the button label during the gap, matching `VenuesSection.tsx`'s intent without needing to duplicate its `refresh()` call (there's no separate refetch here; the store updates itself from the new snapshot).

- [ ] **Step 3: Mount in AppShell** — `ui/src/chrome/AppShell.tsx`: build `const rc = useReplayCommands(commands);` near `useOrderCommands`, and render `<ReplayBanner session={stores.session} engineState={engineState} onGoLive={async () => { await rc.goLive(); }} />` directly above `<FeedStatusBanner .../>` (`engineState` is already in scope — it's passed to `SettingsModal` since `5f61e08`).

- [ ] **Step 4: Verify build/typecheck**

Run: `cd ui && npx tsc --noEmit`
Expected: clean. (Banner render is asserted by the E2E in Task 11.)

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/ReplayBanner.tsx ui/src/chrome/exec/useReplayCommands.ts ui/src/chrome/AppShell.tsx
git commit -m "feat(ui): REPLAY banner with return-to-live"
```

---

## Task 9: UI — Replay launcher dialog + entry point

**Files:**
- Create: `ui/src/chrome/ReplayLauncherModal.tsx`
- Modify: `ui/src/chrome/AppShell.tsx`, `ui/src/chrome/TopBar.tsx`

**Interfaces:** Consumes `useReplayCommands` (Task 8). Produces a modal opened from a TopBar "Practice" button.

- [ ] **Step 1: Modal component** — `ui/src/chrome/ReplayLauncherModal.tsx` (model on `SettingsModal.tsx`: `open` guard → `null`, fixed scrim `onClick={onClose}` `zIndex:10000`, inner box `stopPropagation`, `useTheme()`):

```tsx
import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { ReplayCommandAdapter } from "./exec/useReplayCommands";
import { useReplayCommands } from "./exec/useReplayCommands";

const SPEEDS = [1, 2, 4, 0]; // 0 = max

export function ReplayLauncherModal({ open, onClose, commands }: {
  open: boolean; onClose: () => void; commands: ReplayCommandAdapter;
}): JSX.Element | null {
  const { palette } = useTheme();
  const rc = useReplayCommands(commands);
  const [days, setDays] = useState<string[]>([]);
  const [day, setDay] = useState("");
  const [speed, setSpeed] = useState(1);
  const [starting, setStarting] = useState(false);

  useEffect(() => {
    if (!open) return;
    let live = true;
    rc.listDays().then((d) => { if (live) { setDays(d); setDay(d[0] ?? ""); } });
    return () => { live = false; };
  }, [open, rc]);

  if (!open) return null;
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div data-testid="replay-launcher" onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 380, padding: 20 }}>
        <h3 style={{ marginTop: 0 }}>Practice: replay a recorded day</h3>
        {days.length === 0 ? (
          <p style={{ color: palette.textMuted }}>No recorded days yet.</p>
        ) : (
          <>
            <label style={{ display: "block", marginBottom: 12 }}>Day
              <select data-testid="replay-day" value={day} onChange={(e) => setDay(e.target.value)} style={{ width: "100%" }}>
                {days.map((d) => <option key={d} value={d}>{d}</option>)}
              </select>
            </label>
            <label style={{ display: "block", marginBottom: 16 }}>Speed
              <select data-testid="replay-speed" value={speed} onChange={(e) => setSpeed(Number(e.target.value))} style={{ width: "100%" }}>
                {SPEEDS.map((s) => <option key={s} value={s}>{s === 0 ? "Max" : `${s}×`}</option>)}
              </select>
            </label>
          </>
        )}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <button onClick={onClose}>Cancel</button>
          <button data-testid="replay-start" disabled={!day || starting}
            onClick={() => { setStarting(true); void rc.start(day, speed).finally(onClose); }}>
            {starting ? "Starting…" : "Start replay"}
          </button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Entry point + state** — `ui/src/chrome/TopBar.tsx`: add an `onOpenReplay: () => void` prop and a "Practice" button in the right-side action cluster (near `⚙ Settings`). `ui/src/chrome/AppShell.tsx`: add `const [replayOpen, setReplayOpen] = useState(false);`, pass `onOpenReplay={() => setReplayOpen(true)}` to `<TopBar>`, and render `<ReplayLauncherModal open={replayOpen} onClose={() => setReplayOpen(false)} commands={commands} />` next to `<SettingsModal .../>`.

- [ ] **Step 3: Verify typecheck**

Run: `cd ui && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ui/src/chrome/ReplayLauncherModal.tsx ui/src/chrome/AppShell.tsx ui/src/chrome/TopBar.tsx
git commit -m "feat(ui): replay launcher dialog + Practice entry point"
```

---

## Task 10: UI — PRACTICE badge on the order ticket (safety)

**Files:**
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx`

**Interfaces:** Consumes `SessionStore`.

- [ ] **Step 1: Read `OrderTicketPanel.tsx`.** It's `OrderTicketPanel({ config, stores, commands, ... }: PanelProps)` — `stores.session` is directly available as a prop (once added to the `Stores` type in Task 7). The header row renders `data-testid="venue"`/`"open-settings"` near the top; the badge goes there.

- [ ] **Step 2: Add a badge.** Read `stores.session` via `useSyncExternalStore`; when `mode === "replay"`, render a small `data-testid="practice-badge"` pill ("PRACTICE") in the ticket header, using the same warn color as the banner, so a practice order cannot be mistaken for live.

- [ ] **Step 3: Verify typecheck**

Run: `cd ui && npx tsc --noEmit`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ui/src/chrome/panels/OrderTicketPanel.tsx
git commit -m "feat(ui): PRACTICE badge on order ticket during replay"
```

---

## Task 11: E2E + full verification

**Files:**
- Create: `ui/e2e/replay-launcher.spec.ts`

**Note on E2E scope:** `ui/e2e/serve.sh` already boots the engine with `-replay 2026-01-02 -speed 0 -replay-hold`, so the E2E engine is *already in replay mode*. The E2E therefore asserts the banner + launcher render correctly against a real replay engine. It does **not** exercise the actual relaunch round-trip (that execs/spawns a fresh process — the live leg needs OpenD/creds and is flaky in CI); the round-trip is covered by the manual checklist below, same as the landed `RestartEngine` feature's own verification.

- [ ] **Step 1: Write the E2E spec** — `ui/e2e/replay-launcher.spec.ts` (model on `smoke.spec.ts`):

```ts
import { test, expect } from "@playwright/test";

test("replay engine shows the REPLAY banner and launcher lists the recorded day", async ({ page }) => {
  await page.goto("/?workspace=e2e-replay");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  // serve.sh boots -replay 2026-01-02, so the banner must be present.
  await expect(page.getByTestId("replay-banner")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("replay-banner")).toContainText("2026-01-02");
  // Launcher lists the recorded day from ListReplayDays.
  await page.getByRole("button", { name: /Practice/ }).click();
  await expect(page.getByTestId("replay-launcher")).toBeVisible();
  await expect(page.getByTestId("replay-day")).toContainText("2026-01-02");
});
```

- [ ] **Step 2: Run the full engine suite**

Run: `cd engine && go build ./... && go vet ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 3: Run the full UI unit suite + typecheck + lint**

Run: `cd ui && npx tsc --noEmit && npx vitest run && npm run lint`
Expected: PASS.

- [ ] **Step 4: Run E2E**

Run: `cd ui && npm run e2e -- replay-launcher`
Expected: PASS (banner visible, launcher lists 2026-01-02).

- [ ] **Step 5: Manual verification (relaunch round-trip — not in CI)**

- Live→replay: `./run.sh` (or a live/paper config), open the UI, click **Practice**, pick a day + speed, **Start replay**. Expect: brief disconnect (reconnect overlay), then the REPLAY banner + PRACTICE badge, no new browser tab; place a simulated order and confirm it fills against the replayed book.
- Replay→live: click **Return to live**; expect the button to read "Returning to live…" through the drop, then the banner clears once reconnected.
- Confirm this doesn't regress the existing **RestartEngine** flow (Settings → Venues → "Restart engine now" still works, since both features now share `boot()`'s `restart bool`/`nextArgs` return and the entrypoints' `relaunch(...)` call).
- Windows tray build: confirm the tray icon disappears/reappears cleanly on a mode switch and the app stays functional (flicker acceptable, same as the landed `RestartEngine` behavior).

- [ ] **Step 6: Final commit (if the E2E spec is the only remaining change)**

```bash
git add ui/e2e/replay-launcher.spec.ts
git commit -m "test(e2e): replay banner + launcher against replay engine"
```

---

## Self-Review Notes (spec coverage)

- Mode-switch trigger → Tasks 1-4 (reusing the landed `RestartEngine`/`relaunch()` machinery rather than a separate spawn/lock-retry mechanism). Control plane (`ListReplayDays`, `sys.session`) → Tasks 5-6. UI (store, banner, launcher, badge) → Tasks 7-10. Verification → Task 11.
- Edge cases from the spec: invalid/negative speed and unknown day block synchronously before any relaunch is scheduled (Task 3); ack-before-shutdown ordering preserved via `restartAckFlushDelay` (Tasks 3-4); demo guard (Task 4); no new browser tab on relaunch via forced `-no-open` (Task 1); end-of-day hold via `-replay-hold` (Task 1).
- Deferred to v1.5 (unchanged): pause/scrub/seek, simultaneous live+replay, cross-restart session persistence.

## Execution notes

- **Worktree isolation is strongly recommended** (Global Constraints) — re-check `git log --oneline -5` on local `main` immediately before Task 1 (a concurrent session landed a directly-relevant commit once already during this planning session). Verify `pwd`/branch before each commit.
- After merging, re-run `make gen-ts-check` in case a concurrent session also regenerated the contract.
