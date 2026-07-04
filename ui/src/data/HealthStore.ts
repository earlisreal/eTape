import { ReactStore } from "./store";
import type { HealthLink, HealthSnapshot, SysEvent, SnapshotMsg, DeltaMsg } from "../wire/contract";

interface HealthState { links: HealthLink[]; events: SysEvent[] }
const MAX_EVENTS = 500;

export class HealthStore extends ReactStore<HealthState> {
  constructor() { super({ links: [], events: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    if (m.topic === "sys.health") {
      this.set({ ...cur, links: (m.payload as HealthSnapshot).links });
      return;
    }
    if (m.topic === "sys.events") {
      const incoming = m.kind === "snapshot" ? (m.payload as SysEvent[]) : [m.payload as SysEvent];
      const events = [...cur.events, ...incoming];
      this.set({ ...cur, events: events.slice(Math.max(0, events.length - MAX_EVENTS)) });
    }
  }
}
