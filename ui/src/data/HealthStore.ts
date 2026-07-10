import { ReactStore } from "./store";
import type { HealthLink, HealthSnapshot, SysEvent, SnapshotMsg, DeltaMsg } from "../wire/contract";

interface HealthState { links: HealthLink[]; events: SysEvent[] }
const MAX_EVENTS = 500;

export class HealthStore extends ReactStore<HealthState> {
  // The engine's last sys.health snapshot, verbatim (null-normalized).
  private engineLinks: HealthLink[] = [];
  // The UI's own override for the "ui-engine" link (its WebSocket ping RTT,
  // set by App.tsx) — null until the UI has computed one. The engine's
  // sys.health always reports "ui-engine" as down (a permanent v1 stub on
  // the engine side), so this override is the only source of truth for that
  // one link once set.
  private uiEngine: HealthLink | null = null;

  constructor() { super({ links: [], events: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    if (m.topic === "sys.health") {
      // The engine's zero-value HealthSnapshot (before the first health poll,
      // e.g. every subscriber during a -replay boot) marshals a nil Go slice
      // as JSON null. Normalize to [] so state.links is always an array.
      this.engineLinks = (m.payload as HealthSnapshot).links ?? [];
      this.recompute();
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

  // Sets (or clears, via null) the UI-computed override for the "ui-engine"
  // link. Passing null falls back to whatever the engine's own sys.health
  // snapshot reports for that link.
  setUiEngine(link: HealthLink | null): void {
    this.uiEngine = link;
    this.recompute();
  }

  // Rebuilds the public `links` field from engineLinks plus the uiEngine
  // override, so every consumer (LatencyReadout chips, ConnectionStatusPanel)
  // sees one consistent value for "ui-engine".
  private recompute(): void {
    const cur = this.getSnapshot();
    const override = this.uiEngine;
    const engine = this.engineLinks;
    let links: HealthLink[];
    if (override == null) {
      links = engine;
    } else {
      const hasEntry = engine.some((l) => l.link === "ui-engine");
      links = hasEntry
        ? engine.map((l) => (l.link === "ui-engine" ? override : l))
        : [override, ...engine];
    }
    this.set({ links, events: cur.events });
  }
}
