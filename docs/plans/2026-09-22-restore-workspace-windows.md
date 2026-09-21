# Restore workspace windows across launches

Status: Implemented. Product decisions confirmed through grilling on 2026-09-22; implementation completed on 2026-09-22.

## Goal

On the owned Windows Chrome-app launch path, continuously remember which eTape workspace windows are open and each secondary window's normal bounds. On the next cold launch, automatically recreate the valid workspace windows on their previous monitors, keep the existing maximized main window, and return focus to main.

The target experience is the common two-monitor arrangement: main remains on the primary display and Monitoring reopens at its last size and position on the secondary display.

## Non-goals

- Do not restore News Reader popups, dialogs, minimized state, native z-order, or the last-focused secondary window.
- Do not change workspace layouts, panel state, Link Groups, hotkey targeting, or Monitoring Scanner Sync persistence.
- Do not add a setting, startup prompt, reset-layout control, new dependency, or Chrome-profile session restore.
- Do not promise automatic window restoration through the default-browser fallback, the tray's unowned **Open eTape** action, macOS, or Linux.
- Do not persist cold-launch window state for disposable `-demo` databases; engine self-restarts continue preserving their already-open Chrome process and windows in place.

## Current-code evidence

- [`engine/internal/openbrowser/openbrowser.go`](../../engine/internal/openbrowser/openbrowser.go) starts one isolated Windows Chrome app profile, retains its PID/start token across engine self-restarts, and force-closes the owned process tree only on final shutdown.
- [`engine/internal/openbrowser/process_windows.go`](../../engine/internal/openbrowser/process_windows.go) already finds the startup process's visible native window and maximizes it without applying Chrome's process-wide maximize flag.
- [`engine/cmd/etape/main.go`](../../engine/cmd/etape/main.go) opens the browser only on a cold boot. Self-restarts pass `-no-open` and adopt the existing owned Chrome process, which prevents restoration from duplicating windows during a restart.
- [`ui/src/chrome/windows.ts`](../../ui/src/chrome/windows.ts) gives workspaces stable named targets and currently opens secondary workspaces with `window.open()`. Startup-script `window.open()` is popup-policy-dependent, so it is not a reliable automatic restore mechanism.
- [`ui/src/main.tsx`](../../ui/src/main.tsx) derives the stable workspace id from `?workspace=`. The current working tree also assigns the main workspace its stable target name; implementation must preserve that in-flight change.
- [`ui/src/App.tsx`](../../ui/src/App.tsx) owns one `WsClient` per browser window. [`engine/internal/uihub/conn.go`](../../engine/internal/uihub/conn.go) passes the owning connection id into every command and unregisters it when that exact browser window disconnects.
- [`engine/internal/uihub/hub.go`](../../engine/internal/uihub/hub.go) processes ordinary connection removal while running, but on engine cancellation it closes clients and exits without draining their later unregister calls. That existing distinction can forget a manually closed window while preserving the final open set during engine termination.
- [`engine/internal/store/config.go`](../../engine/internal/store/config.go) already persists versioned JSON configuration per active database. `Store.Close` drains pending writes during the ordered shutdown in [`engine/cmd/etape/main.go`](../../engine/cmd/etape/main.go).
- Chromium's profile singleton forwards a second invocation using the same `--user-data-dir` to the existing browser process, so restored `--app=<workspace-url>` launches remain under the owned instance. Chromium also supports initial `--window-position` and `--window-size` bounds and adjusts new browser bounds against available displays. See Chromium's [`process_singleton.h`](https://chromium.googlesource.com/chromium/src/+/HEAD/chrome/browser/process_singleton.h) and [`browser_window_state.cc`](https://chromium.googlesource.com/chromium/src/+/HEAD/chrome/browser/ui/browser_window_state.cc).

## Design decisions

### Persist presence, not a second window catalog

Use one per-database config document, `window-state.v1`:

```json
{
  "version": 1,
  "entries": [
    { "workspaceId": "monitoring", "x": 1920, "y": 0, "width": 1920, "height": 1040 }
  ]
}
```

An entry's presence means that workspace was open. Do not add an `open` flag, timestamps, monitor identifiers, z-order, or display names. Main is always launched and maximized as it is today; its reported bounds may be retained for a uniform registry but are not used to position it on cold start.

Validate version, workspace id, coordinates, and positive sane dimensions at the engine boundary. Negative coordinates remain valid for monitors left of the primary display. The restore reader intersects custom ids with the existing `windows.v1` catalog and always admits the reserved `monitoring` id. Invalid or deleted entries are removed from the persisted document and logged; they never block main startup.

### Let the connection lifecycle own the open set

Add one typed `SetWindowState` command carrying `workspaceId` and normal bounds. The first call associates the current WebSocket connection id with that workspace; later calls update its bounds. A small mutex-protected registry keeps connection-to-workspace ownership, deduplicates multiple connections for the same workspace, and rewrites `window-state.v1` only when an entry changes.

Ordinary `Hub.handleUnregister` removes that connection. It removes the persisted workspace entry only when no other connection still owns the same workspace. Engine shutdown does not run ordinary unregister handling after the Hub exits, so the last persisted open set remains intact for the next cold launch. No `beforeunload` write, heartbeat expiry, or separate unregister command is needed.

The UI reports `window.screenX`, `window.screenY`, `window.outerWidth`, and `window.outerHeight` outside React state. Send once when the workspace connects, on debounced `resize`, and from a low-rate position poll because browsers have no move event. Compare against the last sent rectangle and do nothing when unchanged; ordinary stationary windows produce no database traffic.

### Restore through the owned launcher

Before the initial browser launch, `boot` reads `window-state.v1` and `windows.v1` from the already-open store, filters and sorts valid secondary entries, and passes their workspace URLs and bounds to the owned-browser adapter. Do this only in the existing `!noOpen` cold-start branch. Adopted self-restarts, an already-running-instance launch, the tray's manual open action, and default-browser fallback keep their current behavior.

Windows launches main first with the existing private profile and waits for its native window before maximizing it. It then starts one Chrome `--app=<workspace-url>` invocation per saved secondary, reusing the same `--user-data-dir` and adding `--window-position=x,y` and `--window-size=width,height`. Chromium's profile singleton forwards those requests into the owned process. After the expected visible windows appear or the bounded wait expires, reuse the captured main HWND to maximize/focus main last.

Use Chromium/Windows native initial-bounds adjustment for a disconnected monitor rather than introducing a second DPI coordinate model in Go. Saved values and Chrome launch flags remain browser CSS-pixel/DIP values. Release validation must include disconnecting the secondary monitor; if Chromium does not move the restored window onto an available work area, add the smallest Windows-only work-area clamp at that adapter boundary before release.

If Chrome is missing or a secondary launch fails, log the failure and continue with main. Do not fall back to startup `window.open()`, show a modal, or delete an otherwise valid saved entry after a transient launch failure.

## File-level implementation

1. In [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go), add the versioned window-state DTOs, bounds DTO, and `SetWindowStateArgs`. Regenerate [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts); never hand-edit the generated file.

2. Add a focused window-state registry beside the Hub in `engine/internal/uihub/` and wire it through [`api.go`](../../engine/internal/uihub/api.go), [`commands.go`](../../engine/internal/uihub/commands.go), and [`hub.go`](../../engine/internal/uihub/hub.go):
   - load and validate `window-state.v1` from the existing config store;
   - associate `SetWindowState` with the calling `connID`;
   - persist only changed entries in deterministic workspace-id order;
   - release an ordinary disconnected connection and remove its workspace only when it was the last owner; and
   - leave the registry untouched on Hub shutdown so final open windows remain recorded.

3. Add a small UI lifecycle module beside [`ui/src/chrome/windows.ts`](../../ui/src/chrome/windows.ts) and start it from [`ui/src/App.tsx`](../../ui/src/App.tsx), where both `workspaceName` and the window's `WsClient` already exist. It must capture browser bounds imperatively, debounce resize, poll position at a low rate, suppress identical updates, and clean up listeners/timers on unmount. Preserve the current uncommitted main-window target and Scanner-focus work in `windows.ts`, `main.tsx`, and their tests.

4. In [`engine/cmd/etape/main.go`](../../engine/cmd/etape/main.go), read the saved state and `windows.v1` after opening the store, construct same-origin workspace URLs with escaped ids, and provide only validated secondary launch specs to `openbrowser.OpenOwned`. Keep main mandatory, Monitoring reserved, custom ids catalog-backed, and self-restart/adoption behavior unchanged. Put parsing/filtering helpers in a small adjacent file only if that keeps `main.go` and its tests materially clearer.

5. In [`engine/internal/openbrowser/openbrowser.go`](../../engine/internal/openbrowser/openbrowser.go), extend the owned launch input with secondary window specs and build repeat Chrome commands using the existing temporary profile. In [`process_windows.go`](../../engine/internal/openbrowser/process_windows.go), reuse the existing bounded HWND polling to capture/maximize main, observe added process windows, and focus main last. Keep [`process_other.go`](../../engine/internal/openbrowser/process_other.go) and fallback launchers main-only. Do not persist or restore Chrome's temporary profile.

6. Update [`engine/internal/openbrowser/README.md`](../../engine/internal/openbrowser/README.md), [`engine/internal/uihub/README.md`](../../engine/internal/uihub/README.md), and [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md) with the per-connection ownership rule, continuous bounds capture, cold-start restore boundary, fallback behavior, and self-restart exception. The root README does not need a new operational step unless implementation changes a user-visible launch instruction.

## Validation

Add focused automated checks proving:

- valid negative-position bounds round-trip, malformed versions/ids/dimensions are rejected, and serialized entries are deterministic;
- the first `SetWindowState` registers the command's connection, unchanged bounds do not rewrite config, and updates replace only that workspace;
- ordinary disconnect removes the last owner, while one of two connections closing keeps the shared workspace entry;
- Hub shutdown preserves the last open set instead of treating engine-driven socket closure as manual window closure;
- deleted custom ids are excluded, Monitoring is admitted without a catalog entry, main is never duplicated, and an absent/invalid document launches main only;
- the UI sends an initial rectangle, coalesces resize, observes position-only moves, suppresses unchanged polls, accepts negative coordinates, and cleans up timers/listeners;
- restored Chrome commands reuse the exact owned profile, carry escaped workspace URLs and saved bounds, and are launched only after the main window is identified;
- secondary-launch failure remains non-fatal, default-browser/non-Windows launch remains main-only, and final focus targets the captured main HWND; and
- engine restart/adoption does not issue restoration launches or duplicate already-open workspaces.

Run the repository's full engine/UI/generated-contract checklist because the change spans both subsystems and the wire contract:

```powershell
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run
mingw32-make gen-ts-check
Set-Location ..\ui
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
git diff --check
```

Release-gating Windows smoke test:

1. Launch the packaged owned-Chrome build with no saved state; verify only maximized main opens.
2. Open Monitoring and a custom workspace, move them across monitors (including a negative-coordinate monitor), resize them, quit from the tray, and relaunch; verify the same valid workspaces return and main finishes focused.
3. Close one secondary manually while the engine remains running, quit, and relaunch; verify only the still-open secondary returns.
4. Restart the engine from the UI; verify existing windows reconnect in place and no duplicates appear.
5. Disconnect the secondary monitor before relaunch; verify its window is moved onto the primary work area. Reconnect it, move the window back, and verify the new placement replaces the fallback placement.
6. Verify a deleted custom workspace and a deliberately malformed `window-state.v1` entry are skipped with warnings while main still opens.
7. Verify final Quit still terminates the entire owned Chrome process tree and removes its temporary profile.

## Rollout and rollback

- The first launch after upgrade has no `window-state.v1`, so behavior is exactly the current single maximized main window. State appears only after workspace windows report their bounds.
- Old binaries ignore the new config key. Rolling back code therefore needs no data migration; the saved document may remain for a later upgrade.
- Version or validation failure degrades to main-only startup and rewrites only the invalid window-state document, never workspace layouts or the window catalog.
- Ship the wire DTOs, registry lifecycle, UI reporter, owned launcher, tests, and documentation together. A partial rollout would either collect state without restoring it or restore stale state without accurate disconnect cleanup.

## Risks

- Chrome profile forwarding and initial-bounds behavior are external browser boundaries. Unit-test command construction and make the packaged Windows multi-window smoke test release-blocking.
- Mixed-DPI monitors can expose CSS-pixel versus native-pixel mistakes. Pass browser coordinates directly to Chrome, avoid Win32 geometry conversion in the initial implementation, and smoke-test monitors with different scaling.
- Windows may decline foreground activation. Capture main before launching secondaries and request focus only after the bounded window-count wait; failure remains logged and non-fatal.
- A transient WebSocket reconnect briefly releases and re-registers a window. The final queued config write wins during normal recovery; add a reconnect grace period only if testing demonstrates user-visible state loss.
- A crash can lose the final fraction of a move between the last changed-position poll and store flush. Continuous changed-only capture bounds that loss without turning window geometry into high-frequency traffic.
