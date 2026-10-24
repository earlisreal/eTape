// Reference-count open dialogs/popups so panel type-to-load and scoped order
// hotkeys stay quiet. useDismissibleLayer owns balanced lifetime registration.
// Dockview freezes panel factory props, so existing panels read this singleton.
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
