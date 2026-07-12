# Demo mode — UI entry (rides the UI-driven-replay control plane)

## Context

The realistic synthetic demo (`synth.Feed`) was approved and split into two chunks
(`docs/superpowers/specs/2026-07-11-demo-synthetic-data-design.md`, §"UI entry" +
§"Sequencing"):

1. **Engine chunk** — the `engine/internal/synth` live-synthetic feed behind `-demo`
   (plan: `docs/superpowers/plans/2026-07-11-demo-synthetic-data-plan.md`). **Built**
   on branch `worktree-demo-synth-data` (locked worktree, 21 commits ahead), **not yet
   merged** to `main`.
2. **UI entry** — deliberately deferred, because it was blocked on the "UI-driven-replay
   control plane" (`StartReplay`/`GoLive`/`sys.session`/`childArgs`/`relaunch`) which at
   the time (2026-07-11) was unmerged.

**That blocker has since cleared.** The control plane is now fully merged on `main`
(confirmed 2026-07-12): `engine/cmd/etape/childargs.go`, `relaunch_unix.go`,
`relaunch_windows.go`, `uihub/commands.go` (`StartReplay`/`GoLive` cases),
`wsmsg/payloads.go` (`StartReplayArgs`/`GoLiveArgs`/`SessionSnapshot`),
`ui/src/chrome/ReplayBanner.tsx`, `ReplayLauncherModal.tsx`,
`ui/src/chrome/exec/useReplayCommands.ts`, `ui/src/data/SessionStore.ts`.

This plan is the **UI entry chunk**: a UI-triggered path into demo mode, a distinct DEMO
mode indicator, and a first-run "Try demo" affordance — so a new user runs the app, sees
an empty live-but-no-feed state, clicks **Try demo**, and lands in a believable synthetic
market. The demo entry is almost entirely a **clone of the replay plumbing**.

## Prerequisite (external — NOT part of this plan)

> **The synth `-demo` engine chunk must be merged into current `main` first.**
> `worktree-demo-synth-data` was cut from `26f6793`, which predates the control-plane
> merge (it even carries an older `relaunch()` with no args), so landing it is a real
> merge/conflict pass on `main.go`. That merge is owned by the worktree session.
>
> After it merges, `main` will have BOTH: (a) the control plane, and (b) `synth.Feed`
> wired behind `-demo` with `-demo-seed` and **no** `-demo-day`/`-demo-speed`. This plan
> targets that post-merge `main`. **Do not start until `git grep -q "internal/synth"
> engine/cmd/etape/main.go` on `main` succeeds** (i.e. synth is wired into boot).

## Design

Three seams, each mirroring the replay equivalent one-for-one:

1. **A `StartDemo` WS command** (peer of `StartReplay`/`GoLive`) that self-restarts the
   engine into `-demo`, via the existing `childArgs`→`nextArgsPtr`→`relaunch` path.
2. **A distinct `"demo"` session mode** on `sys.session` so a dedicated DEMO banner can
   render (today a `-demo` boot would report `"replay"` and show the wrong banner).
3. **Entry surfaces**: a unified Practice launcher (demo | replay), a first-run EmptyState
   CTA, and a button in the no-real-venue prompt.

Mode-switch matrix after this change: `StartDemo` is accepted from live/replay/demo;
`GoLive` is accepted from demo (guard relaxed) so "Return to live" works; `StartReplay`
stays **blocked while in demo** (the real `~/.eTape` journal isn't open in demo — the
temp `demo.db` has only synthetic days, so listing/validating recorded days there is
meaningless). Exit demo via "Return to live" first, then replay a recorded day.

Execution: subagent-driven in a worktree (`superpowers:subagent-driven-development` +
`superpowers:using-git-worktrees`), TDD per task. UI visual work (the DEMO banner, the
unified launcher, the CTAs) uses `frontend-design` — the banner must be **visually
distinct from REPLAY** (different accent, not `palette.warn`) while still unmissable and
clearly "not live".

---

## Engine tasks

### E1 — `StartDemo` command + self-restart + relax `GoLive` guard

**Files:** `engine/internal/uihub/wsmsg/payloads.go`, `engine/internal/uihub/commands.go`
(+`commands_test.go`), `engine/internal/uihub/api.go`, `engine/cmd/etape/childargs.go`
(+`childargs_test.go`), `engine/cmd/etape/main.go`.

- `payloads.go` — add `type StartDemoArgs struct{}` in the replay-control section
  (`:458`), mirroring `GoLiveArgs` (`:469`; kept as a named type for tygo stability).
- `childargs.go` — extend `replayMode` (`:13`) with `Demo bool`; in `childArgs` (`:25`)
  add, before the replay branch, `if mode.Demo { argv = append(argv, "-demo") }`. No
  `-demo-day`/`-demo-speed` (removed in the synth chunk); UI demo takes no knobs.
  Test (`childargs_test.go`): `childArgs(base, replayMode{Demo:true})` yields
  `[-config … -no-open -demo]` and no `-replay`/`-speed`.
- `main.go` — add a `startDemo` closure beside `startReplay`/`goLive` (`:301-336`):
  `argv := childArgs(base, replayMode{Demo:true})`; `time.AfterFunc(relaunchAckFlushDelay,
  …)` stores argv + `requestRestart()`. **Relax the demo guards**: in `goLive` remove the
  `if *demo { return error }` block (`:327-329`) so "Return to live" works from demo;
  leave `startReplay`'s demo guard (`:302-303`) in place (see design). Pass `startDemo`
  into `uihub.New(...)` at the call site (`:410`).
- `api.go` — add a `startDemo func() error` field on the `commands` struct (beside
  `startReplay`/`goLive`, `commands.go:88-89`), add the param to `uihub.New` (`:85`), and
  set it post-construction (`:107-108`).
- `commands.go` — add `case "StartDemo":` in `handle()` beside `GoLive` (`:324`): if
  `cd.startDemo == nil` → `blocked("demo switching not supported")`; else call it and
  return `AckAccepted`. Test (`commands_test.go`): `StartDemo` dispatches to the injected
  closure and acks accepted; `GoLive` now acks accepted when the injected `goLive`
  succeeds (regression guard for the relaxed guard).

### E2 — distinct `"demo"` session mode

**Files:** `engine/cmd/etape/main.go`, `engine/internal/uihub/wsmsg/payloads.go`.

- `main.go` — the `Mode` func passed to `uihub.New` (`:403-408`) returns `"live"`/
  `"replay"`; add `if *demo { return "demo" }` (checked before the `live` branch, since
  in synth demo `live` is false). Confirm `ReplayDay`/`ReplaySpeed` stay empty in demo.
- `payloads.go` — update the `SessionSnapshot` doc comment (`:243-244`) from `"live" or
  "replay"` to include `"demo"`. `Mode` is a plain `string` on the wire, so no struct
  change and no tygo regen needed for the field itself.

---

## UI tasks

### U1 — widen session mode to `"demo"` + regen types + AppShell gates

**Files:** `ui/src/data/SessionStore.ts`, `ui/src/gen/wsmsg.ts` (via `make gen-ts`),
`ui/src/chrome/AppShell.tsx`.

- `SessionStore.ts:8` — widen `SessionState.mode` union to
  `"pending" | "live" | "replay" | "demo"` (update the doc comment above it).
- Regenerate wire types: `cd engine && make gen-ts` (picks up `StartDemoArgs` from E1),
  then `make gen-ts-check` must be clean. `gen/wsmsg.ts` is generated — do not hand-edit.
- `AppShell.tsx` — the venue-setup nudge suppresses during confirmed replay (`:159`,
  `sessionMode.mode !== "replay"`); extend to also suppress during `"demo"`
  (`&& sessionMode.mode !== "demo"`). Same for any other `mode !== "replay"` mode gates.

### U2 — `DemoBanner.tsx` (distinct mode strip) + mount

**Files:** `ui/src/chrome/DemoBanner.tsx` (new, +test), `ui/src/chrome/AppShell.tsx`.

- Clone `ReplayBanner.tsx` → `DemoBanner.tsx`: gate on `s.mode === "demo"` (not
  `"replay"`); copy e.g. `DEMO — synthetic market · practice orders only`; **distinct
  accent** (frontend-design — not `palette.warn`); reuse the exact `sawDropRef` open→
  non-open→open restart-detection for the "Return to live" button, wired to `onGoLive`.
- Mount in the AppShell banner stack right beside `<ReplayBanner>` (`AppShell.tsx:470`),
  passing `session={stores.session}`, `engineState`, and the same `onGoLive` handler
  already defined at `:470-473` (`rc.goLive()` + ack check). Only one of REPLAY/DEMO can
  show (mutually exclusive modes).
- Test: `data-testid="demo-banner"` renders only when `sys.session` mode is `"demo"`;
  "Return to live" invokes `onGoLive`.

### U3 — `startDemo` command hook + unified Practice launcher

**Files:** `ui/src/chrome/exec/useReplayCommands.ts`, `ui/src/chrome/ReplayLauncherModal.tsx`
(→ rename to `PracticeLauncherModal.tsx`), `ui/src/chrome/AppShell.tsx`,
`ui/src/chrome/TopBar.tsx` (label only), + tests.

- `useReplayCommands.ts` — add `startDemo: (): Promise<AckMsg> => cmd.sendCommand("StartDemo", {})`
  alongside `start`/`goLive` (`:12-13`).
- Unify the launcher: rename `ReplayLauncherModal` → `PracticeLauncherModal` with two
  choices — **"Synthetic demo market"** (calls `rc.startDemo()`) and **"Replay a
  recorded day"** (the existing day/speed picker + `rc.start`). Keep the existing
  ack-not-just-resolve error handling (`:39-53`). When there are no recorded days show
  the existing "No recorded days yet" for the replay option; demo is always available.
  Update the wiring in `AppShell.tsx` (`replayOpen`/`setReplayOpen`/`:492`) and the
  TopBar entry copy (`TopBar.tsx:38` "▶ Practice" — keep the button, it now opens the
  unified launcher). No new TopBar button (per decision).
- Tests: launcher renders both options; picking demo sends `StartDemo`; picking replay
  still sends `StartReplay` with day/speed.

### U4 — first-run "Try demo" affordances

**Files:** `ui/src/chrome/EmptyState.tsx`, `ui/src/chrome/VenueSetupPrompt.tsx`
(+its test), `ui/src/chrome/AppShell.tsx`.

- Thread a single `onTryDemo: () => void` callback from AppShell (calls `rc.startDemo()`)
  into both surfaces — keep both components dumb/controlled (VenueSetupPrompt's existing
  contract, `VenueSetupPrompt.tsx:16-18`).
- `EmptyState.tsx` — add a prominent "Try demo" CTA (primary button + one line of copy)
  above/beside the `Catalog`. Gate its visibility on `sessionMode.mode` being `"live"`
  or `"pending"` (not while already in demo/replay) — pass a `showTryDemo` prop from
  AppShell, or hide when already in a practice session.
- `VenueSetupPrompt.tsx` — add a third button "Try demo" beside "Configure venues" /
  "I'll do it later" (`:58-61`); wire `onTryDemo` through. Update its test.

### U5 — retire the CLI demo wrapper + docs

**Files:** delete `etape-demo.cmd`; edit `README.md`, `README-FIRST.txt`, and the
`demo)` help text in `run.sh` if it still advertises a documented demo path.

- Delete `etape-demo.cmd` (demo is now UI-entered; `-demo` survives only as the internal
  relaunch vehicle + power-user shortcut).
- README / README-FIRST: change the new-user story to "run eTape, click **Try demo**".
  Keep any `./run.sh demo` note as a power-user shortcut only.

---

## Verification

Run after each task (per `superpowers:verification-before-completion`):

- **Engine:** `cd engine && go build ./... && go vet ./... && go test ./...` — new
  `commands_test.go`/`childargs_test.go` cases green; existing suites (incl.
  `uihubtest`, replay smoke) unaffected.
- **Wire types:** `cd engine && make gen-ts-check` clean (no drift after `StartDemoArgs`).
- **UI:** `cd ui && npm run test` (vitest) + `npm run build` (tsc) green. Note the canvas
  vitest quirk (run the 5 canvas files individually — `[[etape-ui-canvas-worktree-test-quirk]]`);
  the new banner/launcher/EmptyState/VenueSetupPrompt tests are DOM, not canvas.

**Manual end-to-end** (`./run.sh` live with no OpenD/venues, browser at
`http://127.0.0.1:8686`):

1. First run comes up **live-but-empty** gracefully (feed down, no crash-loop) — the
   pre-"Try demo" state. Venue-setup prompt shows a **Try demo** button; EmptyState shows
   the **Try demo** CTA.
2. Click **Try demo** → engine self-restarts → the distinct **DEMO banner** appears;
   charts warm at all timeframes, DOM breathes, Movers move, a sim order fills against the
   synthetic book (proves the synth chunk is live behind the new entry).
3. Click **Return to live** on the DEMO banner → engine restarts back to the empty live
   state; DEMO banner clears; venue-setup/EmptyState CTAs reappear.
4. Open the unified **Practice** launcher → both "Synthetic demo market" and "Replay a
   recorded day" are offered; each starts its respective mode.
5. Confirm REPLAY and DEMO banners never show simultaneously and are visually distinct.

---

## Sequencing & handoff

- **Gate:** wait until the synth `-demo` chunk is on `main` (Prerequisite check above).
- **Order:** E1 → E2 (engine control plane) → U1 (types/gates) → U2 (banner) → U3
  (hook + launcher) → U4 (first-run CTAs) → U5 (docs cleanup). E-tasks are a small
  dependency chain on `main.go`/`uihub`; U-tasks fan out after U1.
- On approval, save this plan to
  `docs/superpowers/plans/2026-07-12-demo-ui-entry-plan.md` and commit
  `docs(plans): add demo UI entry implementation plan` (per the auto-commit-plans
  convention; do not push). Then execute in a worktree; ask before merging.
