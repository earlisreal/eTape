# Scanner double-click focuses the main window

Status: Implemented; Windows owned-Chrome manual gate pending.

## Goal

When a user double-clicks a Scanner row in any secondary eTape workspace, keep the existing Link Group symbol update and then bring the `main` workspace window in front of other eTape windows and other applications. Restore a minimized main window to its previous normal or maximized state. If main is closed, open it once and reuse that window on later double-clicks.

Apply the behavior on every supported platform through the existing browser-window APIs. Windows owned-Chrome behavior is the release gate because that is the reported workflow.

## Non-goals

- Do not add a Go/WebSocket/Win32 window-control bridge in this change.
- Do not change single-click behavior, Scanner selection/seen state, Link Group routing, or Scanner Sync.
- Do not foreground main when the Scanner is already in the main workspace.
- Do not force maximization, resize, or reposition the main window.
- Do not extend the behavior to Watchlist, Account, or other symbol-opening gestures.
- Do not add a setting or platform-specific preference.

## Current-code evidence

- [`ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx) has one row double-click handler. It marks the row seen and calls `linkGroups.focus(group ?? "green", r.symbol)`, so the activation behavior can be added once without changing symbol routing.
- [`ScannerPanel.test.tsx`](../../ui/src/chrome/panels/ScannerPanel.test.tsx) already proves double-click uses the configured, pinned fallback, and live Link Groups.
- [`linkGroups.ts`](../../ui/src/chrome/linkGroups.ts) broadcasts symbol focus across workspace windows. It intentionally does not activate an OS window.
- [`windows.ts`](../../ui/src/chrome/windows.ts) already owns stable workspace target names, workspace URLs, popup sizing, and `window.open`; its News Reader path demonstrates reusing and focusing a browser window.
- [`main.tsx`](../../ui/src/main.tsx) assigns a stable name only to the Monitoring workspace. The externally launched main window therefore cannot currently be resolved by `workspaceWindowTarget("main")`.
- [`openbrowser`](../../engine/internal/openbrowser/README.md) owns the startup Chrome process and maximizes its first window at launch, but it exposes no runtime restore command and tracks no durable per-workspace native handle.

## Design decisions

### Browser-native activation

Register the main browsing context under the existing `etape-workspace-main` target. On a Scanner double-click outside main, resolve that named context during the user gesture, focus it, and preserve the existing symbol update ordering.

Resolve an existing main window without navigating it. Passing the main URL directly to `window.open` could reload an already-open main workspace when its current query string differs, losing ephemeral UI state. Instead, open the named target without a navigation; only assign the main workspace URL when the returned window is newly blank. If the browser blocks the operation and returns `null`, leave the symbol update intact and do not throw.

`Window.focus()` is browser/OS-controlled. The implementation is acceptable only if the owned Chrome app on Windows restores a minimized main window and raises an obscured main window while preserving its prior geometry. If either manual check fails, stop: do not ship best-effort partial behavior or silently expand this plan into native window control.

### Narrow trigger scope

Keep activation in the Scanner row's existing double-click path, after `linkGroups.focus`. A Scanner hosted by main performs no extra window operation. Monitoring and every custom workspace use the same helper, so there is no workspace-specific branch in `ScannerPanel`.

If main was closed, the same named-target call creates one replacement main window. Subsequent double-clicks reuse it rather than creating duplicates or reloading it.

## File-level implementation

1. In [`ui/src/main.tsx`](../../ui/src/main.tsx), assign the main workspace its existing stable target name while retaining the Monitoring target cleanup behavior. Do not rename custom workspace windows or change URL parsing.

2. In [`ui/src/chrome/windows.ts`](../../ui/src/chrome/windows.ts), add one focused helper that:
   - returns immediately when the caller is already the main workspace;
   - resolves `etape-workspace-main` without navigating an existing context;
   - navigates only a newly created blank context to the main workspace URL;
   - calls `focus()` from the double-click user gesture; and
   - tolerates a blocked `window.open` result without affecting symbol selection.

   Reuse `parseWorkspaceName`, `workspaceWindowTarget`, `workspaceUrl`, and `workspaceWindowFeatures`; add no new abstraction or dependency.

3. In [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx), call the helper after the existing mark-seen and `linkGroups.focus` work. Keep single-click and context-menu handlers unchanged.

4. Extend [`ui/src/chrome/windows.test.ts`](../../ui/src/chrome/windows.test.ts) to cover main no-op, reuse-and-focus without navigation, closed-main creation/navigation/focus, and popup-blocked safety. Extend the existing double-click case in [`ScannerPanel.test.tsx`](../../ui/src/chrome/panels/ScannerPanel.test.tsx) to prove activation is requested without weakening Link Group assertions. Include `windows.test.ts` in the `chrome-regressions` Vitest project so the full UI test command executes the new seam tests.

5. Update [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md) with the named-main-window ownership and Scanner double-click activation behavior. Document the browser-controlled boundary and the Windows manual acceptance gate; no engine or WebSocket documentation changes are needed.

## Validation

Run the proportional UI checks:

```powershell
Set-Location ui
npx vitest run src/chrome/windows.test.ts src/chrome/panels/ScannerPanel.test.tsx
npm run lint
npm run typecheck
Set-Location ..
git diff --check
```

Then verify the release-gating workflow in the normal Windows owned-Chrome app:

1. Open main and a secondary workspace containing a Scanner. Double-click a row and verify the linked symbol reaches main while main becomes the foreground window without a reload.
2. Put another eTape window and a non-eTape application above main. Double-click a Scanner row and verify main rises above both.
3. Minimize main, double-click a Scanner row in the secondary workspace, and verify main restores to the foreground with its previous normal/maximized geometry.
4. Close main, double-click a Scanner row, and verify exactly one main window opens with the selected symbol. Double-click another row and verify the same main window is reused without a page reload.
5. Double-click a Scanner row hosted in main and verify symbol routing still works without opening or focusing another window.

The change passes only if steps 2 and 3 succeed in owned Chrome. If Chrome ignores either foreground request, stop implementation and write a separate native-bridge plan based on explicit per-workspace window identification; do not add that fallback here.

Automated checks pass. The owned-Chrome manual gate remains to be run in a desktop session that exposes the separate owned profile's windows to UI automation.

## Rollout and rollback

- Ship the main-window registration, focus helper, Scanner call site, tests, and UI guide as one UI-only change.
- There is no persisted-data, generated-contract, dependency, engine, or order-flow migration.
- Rollback is a direct revert of the UI-only change. Existing Link Group symbol propagation remains the prior behavior if the activation call is removed.

## Risks

- Browsers and operating systems may reject foreground activation. Keeping the call inside the trusted double-click gesture gives it the strongest browser-supported path; the Windows manual check is release-blocking.
- Opening a named target with a URL can reload an existing window. Resolving the target without navigation and navigating only a new blank context prevents that state loss.
- A blocked popup can return `null`. The helper must fail closed while the already-completed symbol update remains successful.
- A stale or incorrect browsing-context name could create a duplicate main window. Explicit main registration plus reuse/closed-window tests cover the identity boundary.
