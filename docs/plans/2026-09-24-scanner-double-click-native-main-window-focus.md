# Native main-window focus for Scanner double-click

Status: Implemented; automated checks pass; owned-Chrome Windows manual gate pending (no eTape target window was available in this session).

Supersedes the failed Windows release gate in [the browser-only focus plan](2026-09-22-scanner-double-click-focus-main-window.md). The browser path remains the fallback for launches that do not own Chrome.

## Goal

When a Scanner row is double-clicked in a secondary eTape workspace, preserve the existing Link Group symbol update and bring the engine-owned Windows Chrome `main` workspace to the foreground without reloading, resizing, or changing its internal browser focus.

If main is minimized, restore its previous normal or maximized state before requesting foreground activation. If Windows refuses foreground activation, flash main's taskbar button. If main was closed, open exactly one replacement in the same owned Chrome profile, retain its native window handle, and focus it. Repeated requests while replacement is in progress must not create duplicates; Link Group routing independently leaves the latest selected symbol active.

Fallback-browser, tray-opened, and non-Windows launches keep the existing browser-controlled best-effort behavior.

## Non-goals

- Do not guarantee switching Windows virtual desktops.
- Do not use `always-on-top`, `AttachThreadInput`, synthetic input, or other invasive focus-stealing techniques.
- Do not enable Chrome remote debugging or identify main by its mutable title, geometry, enumeration order after startup, or another heuristic.
- Do not build a generic native window registry or expose native handles to the UI.
- Do not add a setting, preference, dependency, or reusable abstraction for a second unrequested native-window action.
- Do not change single-click behavior, Scanner seen state, Link Group routing, Scanner Sync, or symbol selection rollback/error UX.
- Do not extend activation to Watchlist, Account, or other symbol-opening gestures.
- Do not maximize, reposition, or otherwise change main's geometry.
- Do not hand-edit `ui/src/gen/wsmsg.ts`; this command has no payload DTO.

## Preconditions and worktree safety

The current worktree already has unrelated changes in `engine/internal/openbrowser/README.md`, `process_windows.go`, and `process_windows_test.go` that make restored secondary windows maximize. Preserve those edits exactly. Native-focus implementation overlaps the Windows procedure-variable block and its test file, so before implementation is committed, either commit those existing edits separately or obtain explicit authorization to include them. Do not silently fold them into this change.

The current `CONTEXT.md` and `docs/2026-09-20-stop-limit-trigger-state-research.md` changes are unrelated and remain untouched.

## Current-code evidence

- `ui/src/chrome/panels/ScannerPanel.tsx` has one row double-click handler: mark seen, call `linkGroups.focus`, then call `focusMainWorkspace`. This is the only trigger to change.
- `ui/src/chrome/windows.ts` gives main a short-lived presence heartbeat and uses `BroadcastChannel` plus `Window.focus()`. The live-main path never performs native restore/foreground work, cannot observe failure, and is the release gate that failed.
- `ui/src/chrome/panels/ScannerPanel.test.tsx` and `ui/src/chrome/windows.test.ts` replace browser focus with fakes. They prove dispatch but cannot catch the reported Windows failure; both focused suites currently pass.
- `engine/internal/openbrowser/process_windows.go` already identifies main's HWND before restoring any secondary window, but discards it after startup. It already loads `ShowWindowAsync` and `SetForegroundWindow` for cold-start restoration.
- `engine/internal/openbrowser/openbrowser.go` retains the owned Chrome PID, process-start token, private profile, and main URL across engine self-restarts, but carries no native main-window identity.
- `engine/internal/uihub/commands.go` already supports empty-argument commands. `uihub.Config` is the narrow existing seam for injecting boot-owned behavior into command handling.
- `engine/cmd/etape/main.go` constructs the UI hub before a cold-start browser is assigned, so any command callback that reads `startupBrowser` must use synchronized late binding.
- Every Chrome app window currently has the title `eTape`; titles cannot distinguish main from secondary workspaces.

## Design decisions

### One deep owned-browser interface

Add one exported operation, `(*OwnedBrowser).FocusMain() error`, to the `openbrowser` module. Its interface hides native identity validation, minimized-state restoration, foreground denial, taskbar fallback, replacement launch, handle capture, and request serialization. Do not add a one-implementation Go interface or a general window-controller layer.

The method serializes focus/replacement work with one mutex. `Close` participates in the same serialization and marks the browser closed before terminating its process, preventing a late focus request from reopening main during shutdown.

### Retain and validate main HWND

Store main's HWND as `uintptr` inside `OwnedBrowser`. When cold-start restoration captures main before launching secondaries, save that handle instead of discarding it.

Carry the handle through the internal `-owned-browser-main-hwnd` relaunch flag alongside the existing PID, process-start token, profile, and URL. `AdoptOwned` stores the carried value, but `FocusMain` must validate it on every use:

1. Verify the owned Chrome PID still has the recorded process-start token.
2. Verify the HWND still exists.
3. Verify `GetWindowThreadProcessId` maps the HWND to the owned PID.

Never foreground an unvalidated carried or stale handle. A zero handle is unsupported until main has been captured; do not guess among already-open Chrome windows.

### Restore, foreground, and flash

For a valid main HWND:

1. Use `IsIconic` to distinguish a minimized window.
2. Call `ShowWindowAsync(SW_RESTORE)` only when minimized; otherwise use `SW_SHOW`. This avoids normalizing an already maximized main window.
3. Call `SetForegroundWindow`.
4. When `SetForegroundWindow` returns zero, call `FlashWindowEx` with taskbar-only, until-foreground behavior. Treat that as the accepted Windows fallback, not as a reason to roll back symbol routing or show a modal.

Do not resize, reposition, maximize, or toggle topmost state in this path.

### Replace a closed main exactly once

When HWND validation fails, keep the same focus mutex held and:

1. Verify the owned Chrome process identity.
2. Snapshot its visible HWNDs.
3. launch the stored main URL through the same Chrome executable and private profile;
4. wait for one newly visible HWND owned by the same PID, using the existing before/after capture pattern;
5. save the replacement handle; and
6. run the normal restore/foreground/flash flow.

The mutex makes simultaneous requests converge on the first captured replacement. Later callers validate and reuse it. If Chrome cannot launch or the new HWND is not captured within the existing bounded wait, return an error for engine logging; keep the symbol change successful.

Store the discovered Chrome executable path in `OwnedBrowser` on cold launch. On adoption, rediscover it for replacement launches; focusing an existing valid HWND does not depend on rediscovery succeeding.

### Command acknowledgment chooses native or browser path

Add the empty-argument `FocusMainWorkspace` command. Add `FocusMainWorkspace func() bool` to `uihub.Config`; `true` means the owned-browser request was queued, while `false` means native ownership is unavailable. The handler returns `accepted` only for `true`, otherwise `blocked`. No generated DTO is needed.

In `main.go`, replace unsynchronized cross-goroutine access to `startupBrowser` with `atomic.Pointer[openbrowser.OwnedBrowser]`. The injected callback:

- returns `false` when there is no owned browser;
- coalesces an already-pending request with one `atomic.Bool` and still returns `true`;
- otherwise starts one goroutine that calls `FocusMain`, logs any error, and clears the pending flag.

In the UI, deepen `focusMainWorkspace` so its single call owns path selection:

1. return immediately in the main workspace;
2. send `FocusMainWorkspace`;
3. when accepted, do no browser window operation; and
4. when blocked or rejected, run the existing heartbeat/`BroadcastChannel`/named-window fallback.

Waiting for the acknowledgment avoids a race where the browser and engine both open a replacement main window. Arbitrary-browser popup creation becomes best effort after the acknowledgment, which is acceptable outside the engine-owned Windows release target. Existing-main browser fallback remains unaffected because `BroadcastChannel` does not require popup creation.

Keep the Scanner ordering as mark seen, publish Link Group focus, then request main activation. The activation result never changes Link Group state.

## File-level implementation

1. **Owned-browser state and relaunch identity** — `engine/internal/openbrowser/openbrowser.go`, `engine/internal/openbrowser/openbrowser_test.go`
   - Add the focus mutex, closed state, Chrome executable path, and main HWND to `OwnedBrowser`.
   - Save the cold-start main HWND reported by the Windows restoration path.
   - Extend `AdoptOwned` and `RelaunchArgs` with the HWND and cover zero/nonzero relaunch identity.
   - Serialize `FocusMain` and `Close` so shutdown cannot race replacement launch.

2. **Windows adapter** — `engine/internal/openbrowser/process_windows.go`, `engine/internal/openbrowser/process_windows_test.go`
   - Extend cold-start capture to return/store main's HWND before secondary restoration.
   - Add the smallest injectable Win32 call seams needed for `IsWindow`/PID validation, `IsIconic`, `SetForegroundWindow`, and `FlashWindowEx`, following the file's existing procedure-call-variable pattern.
   - Implement valid-window activation, denial flashing, stale-window replacement/capture, and error reporting behind `OwnedBrowser.FocusMain`.
   - Reuse `visibleProcessWindows`, `waitNewVisibleProcessWindow`, the stored URL/profile, and existing Chrome command builders.
   - Preserve the current uncommitted restored-window maximization behavior and its test.

3. **Portable adapter** — `engine/internal/openbrowser/process_other.go`
   - Match any changed internal function signatures and return a clear unsupported error for native focus. Do not add non-Windows behavior.

4. **Engine wiring** — `engine/cmd/etape/main.go`
   - Parse `-owned-browser-main-hwnd` and pass it to `AdoptOwned`.
   - Store/load `startupBrowser` through `atomic.Pointer` everywhere it crosses command, restart, launch, and shutdown paths.
   - Inject the coalescing `FocusMainWorkspace func() bool` callback into `uihub.Config` and log asynchronous `FocusMain` errors once per attempted request.

5. **UI hub command** — `engine/internal/uihub/api.go`, `engine/internal/uihub/commands.go`, `engine/internal/uihub/commands_test.go`
   - Add the callback to `Config` and wire it to `commands`.
   - Handle `FocusMainWorkspace` with no payload: accepted when queued, blocked when unsupported.
   - Add it to the command regression sweep and cover supported, unsupported, and coalesced callback behavior without coupling tests to Win32.

6. **UI trigger and fallback selection** — `ui/src/chrome/windows.ts`, `ui/src/chrome/windows.test.ts`, `ui/src/chrome/panels/ScannerPanel.tsx`, `ui/src/chrome/panels/ScannerPanel.test.tsx`
   - Give `focusMainWorkspace` the existing command sender as its only new dependency.
   - No-op in main; otherwise prefer an accepted native command and invoke the current browser fallback only after blocked/rejected acknowledgment.
   - Keep one Scanner double-click call after `linkGroups.focus`.
   - Cover main no-op, accepted-native/no-browser-fallback, blocked/rejected browser fallback, and Link Group-before-activation behavior.

7. **Durable documentation** — `engine/internal/openbrowser/README.md`, `engine/internal/uihub/README.md`, `ui/src/chrome/README.md`
   - Document HWND ownership/relaunch validation, the empty command, taskbar denial fallback, exactly-one replacement, and best-effort non-owned browsers.
   - Replace the UI guide's obsolete browser-controlled Windows release-gate wording.
   - Do not add a domain glossary term or ADR; this extends an existing window-ownership decision without changing trading semantics or establishing a competing architecture.

## Test-first and validation plan

### Red-capable checks before the fix

1. Add a Scanner/UI helper regression that expects an accepted `FocusMainWorkspace` command to suppress browser fallback; watch it fail because no command is currently sent.
2. Add UI hub tests that expect supported/unsupported command acknowledgments; watch them fail as unknown commands.
3. Add Windows `openbrowser` tests that exercise a valid minimized HWND, foreground denial, stale HWND replacement, duplicate-request serialization, and carried-handle validation through injected native calls; watch them fail before implementation.

The real OS foreground symptom has no deterministic headless seam. Automated tests cover every decision and native call boundary, while the owned-Chrome workflow below remains release-blocking.

### Focused automated checks

```powershell
Set-Location engine
go test ./internal/openbrowser ./internal/uihub ./cmd/etape
Set-Location ..\ui
npx vitest run src/chrome/windows.test.ts src/chrome/panels/ScannerPanel.test.tsx
Set-Location ..
mingw32-make -C engine gen-ts-check
git diff --check
```

### CI-equivalent checks

Because this change spans engine, UI, startup flags, native Windows behavior, and the WebSocket command surface, run the complete Windows checklist in `README.md#ci-equivalent-validation-on-windows`, plus `npm run e2e`. Verify Go files retain LF endings and `ui/src/gen/wsmsg.ts` has no drift.

### Owned-Chrome Windows release gate

Run from a normal eTape cold launch that owns its private Chrome profile:

1. Put main behind both a secondary eTape workspace and a non-eTape application. Double-click a Scanner row; verify the symbol reaches main and main comes to the foreground without reload.
2. Minimize main from a normal state, double-click, and verify it restores in the foreground with the same normal geometry.
3. Maximize then minimize main, double-click, and verify it returns maximized rather than normalized.
4. Exercise a Windows foreground-denial case where possible; verify main's taskbar button flashes and symbol routing still succeeds.
5. Close main, rapidly double-click several Scanner rows, and verify exactly one replacement opens in the same profile, the latest symbol wins, and later double-clicks reuse the captured window.
6. Restart the engine in place, then double-click from Scanner and verify the carried HWND still restores/focuses the existing main.
7. Double-click a Scanner hosted in main and verify no native command or browser focus/open occurs.
8. Run without an owned browser (tray/manual or fallback browser) and verify the existing browser path still focuses a live main best effort and does not affect symbol routing when focus/open is declined.

The change does not pass if obscured or minimized owned Chrome fails to foreground without producing the specified taskbar fallback, if maximized state is lost, if main reloads, or if a closed-main request creates duplicates.

## Rollout and rollback

- Ship the command, native ownership state, UI path selection, tests, and subsystem guide updates together.
- No database, user configuration, workspace document, generated payload, credential, or order-flow migration is required.
- The extra relaunch flag is internal and optional; a zero/missing HWND blocks native focus rather than guessing.
- Rollback removes the command and retained HWND fields/flag, returning Scanner activation to the existing browser helper while leaving Link Group routing unchanged.

## Risks

- **Windows can deny foregrounding.** Detect the zero return and flash the taskbar; do not escalate to invasive focus stealing.
- **HWND reuse could target another window.** Validate both the owned process-start token and HWND-to-PID mapping immediately before use.
- **Main can close between validation and activation.** Treat failed/stale activation as a bounded replacement attempt under the same mutex.
- **An unrelated owned Chrome window could appear during replacement capture.** The before/after set, same-PID check, serialized engine launch, and short capture interval minimize this existing Chrome limitation; the closed-main manual gate must try rapid repeated input and opening another eTape popup.
- **Asynchronous command fallback could lose browser popup permission.** This affects only non-owned browsers, which are explicitly best effort; existing-live-main `BroadcastChannel` focus does not need popup permission.
- **Engine self-restart can carry a stale handle.** Validation is mandatory after adoption; never trust the numeric flag alone.
- **Shutdown can race a queued focus request.** Serialize `Close` with focus/replacement and reject focus after the closed state is set.
- **Existing uncommitted openbrowser edits overlap implementation.** Resolve their ownership before committing; never overwrite or silently include them.
