// Tracks whether the (single, app-wide) Settings modal is currently open, so
// PanelFrame's type-to-load keydown capture can suppress itself while a modal
// has the user's attention.
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
// consumed) rather than a plain value prop. There's only ever one Settings
// modal in the whole app, so a shared singleton is simpler than instantiating
// a per-App instance in App.tsx's useMemo and threading it through every
// panel-construction site just for this one boolean.
const subs = new Set<() => void>();
let open = false;

export const modalTracker = {
  setOpen(v: boolean): void {
    if (v === open) return;
    open = v;
    subs.forEach((cb) => cb());
  },
  isOpen(): boolean {
    return open;
  },
  subscribe(cb: () => void): () => void {
    subs.add(cb);
    return () => subs.delete(cb);
  },
};
