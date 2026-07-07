import { ReactStore } from "./store";
import type { HealthLink, HealthSnapshot, SysEvent, SnapshotMsg, DeltaMsg } from "../wire/contract";

interface HealthState { links: HealthLink[]; events: SysEvent[] }
const MAX_EVENTS = 500;

export class HealthStore extends ReactStore<HealthState> {
  constructor() { super({ links: [], events: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    if (m.topic === "sys.health") {
      // The engine's zero-value HealthSnapshot (before the first health poll,
      // e.g. every subscriber during a -replay boot) marshals a nil Go slice
      // as JSON null. Normalize to [] so state.links is always an array.
      const links = (m.payload as HealthSnapshot).links ?? [];
      this.set({ ...cur, links });
      return;
    }
    if (m.topic === "sys.events") {
      // Same zero-value story as sys.health: the engine's nil `events` slice
      // (before the first event is ever recorded, e.g. every subscriber during
      // a -replay boot with no sys events) marshals as JSON null. Normalize to
      // [] rather than wrapping it as a single (null) event.
      const incoming =
        m.payload == null ? [] : Array.isArray(m.payload) ? (m.payload as SysEvent[]) : [m.payload as SysEvent];
      const events = [...cur.events, ...incoming];
      this.set({ ...cur, events: events.slice(Math.max(0, events.length - MAX_EVENTS)) });
    }
  }
}
