# Dockview 8 custom Panel Headers

Status: Approved on 2026-08-14.

## Goal

Make the existing eTape Panel Header the visible Dockview tab surface so its unused background can start native Dockview drag-and-drop. A one-panel Panel Group shows that full header across the group; a group with multiple panels shows one full header per selectable Tab. Upgrade the UI to Dockview 8.1, retain the built-in presets, and safely discard incompatible saved layout state.

## Non-goals

- Do not add Dockview Enterprise features, keyboard navigation, tab groups, pinning, context menus, tab wrapping, or history/compass/edge-group features.
- Do not write a v7-to-v8 serialized-layout migration or preserve legacy panel/link focus state.
- Do not replace Dockview's native merge, split, floating/popout, or overflow behavior.
- Do not duplicate header state, remount canvas-backed panel bodies, or change Link Group semantics.

## Current-code evidence

- [`AppShell.tsx`](../../ui/src/chrome/AppShell.tsx) supplies only panel body components to `DockviewReact`; `syncTabVisibility` hides a group's Dockview header when it contains one panel.
- [`PanelFrame.tsx`](../../ui/src/chrome/PanelFrame.tsx) owns the complete live header: active state, symbol and Link Group state, type-to-load, controls, close action, and header-action portals. Its body must remain mounted while Dockview moves a panel.
- [`presets.ts`](../../ui/src/chrome/presets.ts) serializes `hideHeader: true` on singleton leaves, which would hide the new visible Panel Header.
- [`workspace.ts`](../../ui/src/chrome/workspace.ts) persists a layout, panel list, and optional Link Group focus state without a layout-version marker. [`backup.ts`](../../ui/src/chrome/backup.ts) currently accepts an imported workspace directly after reconciliation.
- Dockview 8 supports a custom React tab renderer and full-width single tabs, while its v8 migration notes call out the changed renderer height contract. [Custom tabs](https://dockview.dev/docs/core/panels/tabs/) and the [v8 migration guide](https://dockview.dev/docs/releases/migrating/migrating-to-v8/) are the implementation references.

## Design decisions

### Native surface, eTape-owned content

Configure one `defaultTabComponent` for every panel and set Dockview's `singleTabMode` to `fullwidth`. Dockview keeps ownership of the outer tab element, activation, drag targets, merging, splitting, floating/popout, and its standard overflow menu. The custom renderer supplies only the eTape header content host.

Keep `PanelFrame` as the sole state and body owner. Extend the existing [`panels/headerSlot.ts`](../../ui/src/chrome/panels/headerSlot.ts) seam with a small, per-Dockview-instance header-host context. The custom tab registers its DOM host by panel id; `PanelFrame` portals its existing header JSX into that host. It falls back to inline rendering only when no host exists, preserving focused unit tests and avoiding a second header implementation.

The normal tab location receives the live header. An overflow copy receives a compact, non-interactive label and never becomes another portal host, preventing duplicated controls while retaining Dockview's standard overflow selection. Header controls keep their current event handling so clicking a selector, action, or close button does not begin a drag; unused header space remains draggable.

Remove `syncTabVisibility` and all preset `hideHeader` flags. The custom tab itself is now the header: full-width for a singleton, and Dockview's normal tab strip for multiple panels. Preserve active styling by applying the focused state to the portaled header as well as the panel body.

### Versioned workspace reset

Add `WORKSPACE_LAYOUT_VERSION = 8` and a required `layoutVersion` field to `Workspace`. A new workspace is blank and already version 8. On loading an unmarked or older workspace, replace it once with a blank version-8 workspace and persist it immediately, with no panels, serialized layout, Link Groups, or focused venues. This is the accepted durable decision in [ADR 0001](../adr/0001-reset-legacy-dockview-layouts.md).

Built-in presets remain trusted, version-8 inputs: applying one after the reset recreates its panels and serialized layout. Imported layouts are not reset or migrated. After the existing import-envelope and shape checks, an imported workspace must declare layout version 8; otherwise neither import path applies it and the user sees exactly `Invalid layout`. The separate hotkey import path remains unchanged.

### Dockview 8 scope

Upgrade `dockview`, `dockview-react`, and the development-only `dockview-core` package to 8.1. Use only the required single-tab presentation. Do not opt into keyboard navigation or other newly available features. Validate canvas sizing because v8 excludes header height from renderer dimension callbacks; eTape's panel body `ResizeObserver` is expected to remain the source of truth.

## File-level implementation

1. Update [`ui/package.json`](../../ui/package.json) and [`ui/package-lock.json`](../../ui/package-lock.json) to Dockview 8.1. Keep the existing package split and do not add a new dependency.

2. In [`ui/src/chrome/AppShell.tsx`](../../ui/src/chrome/AppShell.tsx):
   - provide the header-host context around `DockviewReact`;
   - register the generic custom tab renderer as `defaultTabComponent` and configure full-width singleton tabs;
   - remove `syncTabVisibility` and its layout-change calls;
   - preserve the stable panel-body factories, `fromJSON`/`toJSON` lifecycle, native DnD behavior, and close bookkeeping; and
   - route invalid imported layouts to the exact `Invalid layout` message without calling `applyWorkspace`.

3. In [`ui/src/chrome/PanelFrame.tsx`](../../ui/src/chrome/PanelFrame.tsx) and [`ui/src/chrome/panels/headerSlot.ts`](../../ui/src/chrome/panels/headerSlot.ts):
   - add the smallest host registration/lookup context scoped to the current Dockview instance;
   - portal the existing `.ledger-header` JSX to its registered tab host while retaining all existing state, test ids, slots, actions, and body ownership;
   - retain an inline fallback for standalone tests; and
   - make the active/focused header styling work across the portal boundary.

4. Add the small custom tab host component beside the Chrome panel code. It registers only the primary tab host, uses a compact passive overflow representation, and disposes registrations when Dockview moves or removes a tab. Do not persist a per-panel `tabComponent` name: the one default renderer covers both new panels and restored/preset layouts.

5. In [`ui/src/global.css`](../../ui/src/global.css), make the portaled header fill its tab host and retain the existing compact layout/focus visuals. Adjust only styles necessary for the new ownership boundary; do not restyle the panel chrome.

6. In [`ui/src/chrome/presets.ts`](../../ui/src/chrome/presets.ts) and its Dockview tests, regenerate or normalize the built-in serialized layouts under 8.1 and remove `hideHeader`. Verify every shipped preset restores through the upgraded API and exposes its Panel Headers.

7. In [`ui/src/chrome/workspace.ts`](../../ui/src/chrome/workspace.ts) and [`ui/src/chrome/NewWindowModal.tsx`](../../ui/src/chrome/NewWindowModal.tsx):
   - centralize creation of a blank current-version workspace;
   - reset and immediately mark legacy stored workspaces on load; and
   - ensure direct workspace application cannot reintroduce an unversioned persisted workspace.

8. In [`ui/src/chrome/backup.ts`](../../ui/src/chrome/backup.ts), [`ui/src/chrome/BackupPanel.tsx`](../../ui/src/chrome/BackupPanel.tsx), and the empty-state import path in [`AppShell.tsx`](../../ui/src/chrome/AppShell.tsx), reject non-v8 layout payloads with the same `Invalid layout` result before reconciliation/application. Keep export of a current workspace and independent hotkey import working.

9. Update [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md) with the header-host ownership, native DnD responsibility, version-8 reset behavior, and import rule. Retain the glossary additions in [`CONTEXT.md`](../../CONTEXT.md).

## Validation

Start with focused UI checks, then run the dependency-change checklist required by [`AGENTS.md`](../../AGENTS.md):

```powershell
Set-Location ui
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run
Set-Location ..
mingw32-make -C engine gen-ts-check
git diff --check
```

Add or adapt tests to prove:

- a singleton group renders a full-width, draggable Panel Header rather than hiding it;
- adding a second panel creates selectable headers, with the existing header controls, symbols, Link Groups, actions, and close behavior still live;
- focus changes and Dockview drag/reparenting retain the same panel-body DOM node and do not remount a chart, ladder, or tape body;
- overflow does not duplicate interactive header controls; default Dockview merge, split, floating/popout, and close behavior remain available;
- each built-in preset round-trips under Dockview 8.1 and shows its headers;
- an unmarked stored workspace clears panels, layout, Link Groups, and venues exactly once, then a preset works normally;
- current version-8 workspace exports import successfully, while legacy/missing-version layout imports show exactly `Invalid layout` and leave the current workspace untouched; and
- hotkey-only imports still succeed independently of layout validity.

Inspect visual behavior manually at narrow and wide widths, including a chart, tape, ladder, a multi-panel group, drag-to-split, merge, floating/popout, close, and a restored built-in preset. Check each canvas panel's visible body height after the v8 upgrade.

## Rollout and rollback

- Release the dependency upgrade, header renderer, workspace marker/reset, presets, tests, and documentation as one change; partial rollout would leave incompatible saved data or invisible headers.
- Existing legacy local layouts reset on first load. Users can immediately choose a built-in preset or build a fresh layout.
- A legacy import is refused rather than modified; the user can import hotkeys separately.
- If a v8 regression appears before release, revert the change as one unit. Do not keep a partial compatibility migration or a mixed v7/v8 workspace state.

## Risks

- Dockview may mount/unmount a renderer during drag, overflow, or popout. Host cleanup and the body-identity integration test prevent stale hosts and accidental panel remounts.
- Portaling moves active styling and popovers across DOM boundaries. Focus, picker, close, and portal-action tests plus manual narrow-width testing cover the meaningful interactions.
- The layout reset is intentionally destructive. Its one-time per-workspace marker, built-in presets, explicit ADR, and import refusal make the boundary deterministic rather than silently corrupting a saved layout.
- Dockview 8 changes reported renderer dimensions. The existing body observer is expected to insulate canvas panels, but their visible height is a release-blocking manual verification.
