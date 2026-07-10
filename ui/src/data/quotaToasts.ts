import type { SnapshotMsg, DeltaMsg, SysEvent } from "../wire/contract";
import type { ToastApi, ToastLevel } from "../chrome/Toast";

interface EventSubscriber {
  subscribe(topic: "sys.events", cb: (m: SnapshotMsg | DeltaMsg) => void): () => void;
}

// connectEventToasts is the generic engine-event → toast bridge: any warn/
// danger sys.event (currently only quota-contention transitions) raises one
// toast. Only DELTA messages toast — the hub replays event history as a
// SNAPSHOT on every (re)subscribe, so toasting snapshots would re-fire old
// transitions on reload/reconnect. Deduped by kind+seq+ts so a redelivered
// delta toasts at most once. Returns the unsubscribe disposer.
export function connectEventToasts(client: EventSubscriber, toast: ToastApi): () => void {
  const seen = new Set<string>();
  return client.subscribe("sys.events", (m) => {
    const events = asEvents(m.payload);
    if (m.kind === "snapshot") {
      for (const e of events) seen.add(keyOf(e)); // history: mark seen, never toast
      return;
    }
    for (const e of events) {
      const k = keyOf(e);
      if (seen.has(k)) continue;
      seen.add(k);
      const lvl = e.level;
      if (lvl === "warn" || lvl === "danger") {
        toast.push({ level: lvl as ToastLevel, text: e.detail });
      }
    }
  });
}

function asEvents(payload: unknown): SysEvent[] {
  if (payload == null) return [];
  return Array.isArray(payload) ? (payload as SysEvent[]) : [payload as SysEvent];
}

function keyOf(e: SysEvent): string {
  return `${e.kind}::${e.seq}::${e.ts}`;
}
