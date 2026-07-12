import { useEffect, useRef } from "react";

// Fires at most once per UI session: on the first render where `ready` is
// true, if the setting is enabled and trading isn't already armed, unlocks
// it. The `done` ref is a one-way latch — once tripped it never re-fires,
// even if `enabled`/`armed` change afterward (a manual lock, KILL, the
// engine's day-loss auto-disarm, or a later reconnect must never cause a
// silent re-arm).
export function useAutoUnlockOnStartup(p: {
  ready: boolean; enabled: boolean; armed: boolean; onUnlock: () => void;
}): void {
  const done = useRef(false);
  useEffect(() => {
    if (done.current || !p.ready) return;
    done.current = true;                         // latch on first ready, regardless
    if (p.enabled && !p.armed) p.onUnlock();
  }, [p.ready, p.enabled, p.armed, p.onUnlock]);
}
