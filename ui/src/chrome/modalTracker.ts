// Tracks whether ANY modal-ish surface (the app-wide Settings modal, and now
// TVDialog-based dialogs mounted inside chart panels) is currently open, so
// PanelFrame's type-to-load keydown capture can suppress itself while one of
// them has the user's attention.
//
// Why a module-level singleton rather than a prop/context value threaded down
// from AppShell: dockview creates each panel's React content ONCE at
// panel-creation time and never re-invokes the factory closure AppShell hands
// it on later AppShell re-renders (see the `api` comment in PanelFrame.tsx —
// verified against this dockview version for Task 12's `active` prop). A
// `modalOpen` prop threaded the same way would freeze at whatever value was
// true when the panel was created, same bug, different field. Anything a
// frozen-at-creation PanelFrame needs to observe live has to be a *stable
// object* it subscribes to post-mount (exactly how `linkGroups` itself is
// consumed) rather than a plain value prop. A shared singleton is simpler
// than instantiating a per-App instance in App.tsx's useMemo and threading it
// through every panel-construction site just for this one boolean.
//
// Reference-counted, not a plain boolean: AppShell's Settings modal
// (`AppShell.tsx`, driven off `settings.open`) and every TVDialog instance
// (`panels/tv/TVDialog.tsx`) each call `setOpen(true)`/`setOpen(false)`
// independently around their own open lifetime. If two of them are open at
// once (e.g. Settings open, then a chart-panel dialog opened on top) and the
// dialog closes first, that must NOT re-arm type-to-load while Settings still
// has focus — so "closed" only happens once every open caller has also
// called `setOpen(false)`. The public shape stays a boolean in/out
// (`setOpen(v: boolean)`, `isOpen(): boolean`) so every existing caller is
// untouched; only the internal representation is a counter.
const subs = new Set<() => void>();
let count = 0;

export const modalTracker = {
  setOpen(v: boolean): void {
    const wasOpen = count > 0;
    // Floored at 0: defensive against an unbalanced extra `false` (e.g. a
    // stray cleanup call) — the counter must never go negative and require
    // an extra compensating `true` just to look "open" again.
    count = v ? count + 1 : Math.max(0, count - 1);
    const isOpenNow = count > 0;
    if (isOpenNow !== wasOpen) {
      subs.forEach((cb) => cb());
    }
  },
  isOpen(): boolean {
    return count > 0;
  },
  subscribe(cb: () => void): () => void {
    subs.add(cb);
    return () => subs.delete(cb);
  },
};
