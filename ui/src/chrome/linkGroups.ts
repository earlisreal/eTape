import type { AckMsg, VenueID } from "../wire/contract";

export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
export interface LinkMsg { group: LinkGroup; symbol?: string; venue?: VenueID }

export interface LinkBus {
  post(msg: LinkMsg): void;
  onMessage(cb: (msg: LinkMsg) => void): () => void;
  close(): void;
}

export class BroadcastChannelBus implements LinkBus {
  private ch = new BroadcastChannel("etape.link");
  post(msg: LinkMsg): void { this.ch.postMessage(msg); }
  onMessage(cb: (msg: LinkMsg) => void): () => void {
    const handler = (e: MessageEvent) => cb(e.data as LinkMsg);
    this.ch.addEventListener("message", handler);
    return () => this.ch.removeEventListener("message", handler);
  }
  close(): void { this.ch.close(); }
}

// Per-group focused symbol. Local focus publishes cross-window + echoes to the
// engine; remote focus (from the bus) updates state but never re-publishes.
export class LinkGroups {
  private readonly focused = new Map<Exclude<LinkGroup, null>, string>();
  private readonly focusedVenues = new Map<Exclude<LinkGroup, null>, VenueID>();
  private readonly subs = new Set<() => void>();

  constructor(
    private readonly bus: LinkBus,
    private readonly onEcho: (group: Exclude<LinkGroup, null>, symbol: string) => Promise<AckMsg> | void,
  ) {
    this.bus.onMessage((msg) => {
      if (!msg.group) return;
      if (msg.symbol !== undefined) this.setLocal(msg.group, msg.symbol);
      if (msg.venue !== undefined) this.setLocalVenue(msg.group, msg.venue);
    });
  }

  focus(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.setLocal(group, symbol);
    this.bus.post({ group, symbol });
    this.onEcho(group, symbol);
  }

  /**
   * Validate with the engine BEFORE moving the group; returns `{ ok: false }`
   * (group left completely unchanged — no partial/half-switched state) on a
   * rejecting ack. This is the type-to-load commit path for grouped panels;
   * `focus` above remains the synchronous remote-bus-echo path (its ack, if
   * any, is intentionally not awaited there).
   */
  async focusChecked(group: Exclude<LinkGroup, null>, symbol: string): Promise<{ ok: true } | { ok: false; reason: string }> {
    const ackP = this.onEcho(group, symbol);
    const ack = ackP ? await ackP : { status: "accepted" as const };
    if (ack.status !== "accepted") return { ok: false, reason: ack.reason ?? "symbol rejected" };
    this.setLocal(group, symbol);
    this.bus.post({ group, symbol });
    return { ok: true };
  }

  private setLocal(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.focused.set(group, symbol);
    this.subs.forEach((cb) => cb());
  }

  symbolFor(group: LinkGroup): string | undefined {
    return group ? this.focused.get(group) : undefined;
  }

  // Venue focus is UI-only state — unlike symbol focus it does NOT echo to the
  // engine (the engine has no per-group venue concept). It publishes cross-window
  // and notifies subscribers so grouped panels re-render.
  focusVenue(group: Exclude<LinkGroup, null>, venue: VenueID): void {
    this.setLocalVenue(group, venue);
    this.bus.post({ group, venue });
  }

  venueFor(group: LinkGroup): VenueID | undefined {
    return group ? this.focusedVenues.get(group) : undefined;
  }

  private setLocalVenue(group: Exclude<LinkGroup, null>, venue: VenueID): void {
    this.focusedVenues.set(group, venue);
    this.subs.forEach((cb) => cb());
  }

  subscribe(cb: () => void): () => void { this.subs.add(cb); return () => this.subs.delete(cb); }

  /**
   * Seed the focused map from a persisted workspace doc (AppShell load, before
   * any panel mounts and reads symbolFor) — deliberately silent: no bus post,
   * no engine echo, no subscriber notification. This is a page-load restore of
   * state the engine already agreed to earlier, not a new focus decision, so it
   * must not re-validate with the engine, re-broadcast to other windows, or
   * trigger the persistence subscriber that would otherwise immediately
   * re-save (and race) the very doc it was just read from.
   */
  hydrate(map: Partial<Record<Exclude<LinkGroup, null>, string>>): void {
    for (const [group, symbol] of Object.entries(map) as [Exclude<LinkGroup, null>, string][]) {
      if (symbol) this.focused.set(group, symbol);
    }
  }

  /** Plain-object snapshot of the focused map, for persisting into the workspace doc. */
  snapshot(): Partial<Record<Exclude<LinkGroup, null>, string>> {
    return Object.fromEntries(this.focused);
  }

  /** Seed per-group focused venues from a persisted workspace doc (silent — see hydrate). */
  hydrateVenues(map: Partial<Record<Exclude<LinkGroup, null>, VenueID>>): void {
    for (const [group, venue] of Object.entries(map) as [Exclude<LinkGroup, null>, VenueID][]) {
      if (venue) this.focusedVenues.set(group, venue);
    }
  }

  /** Plain-object snapshot of the focused-venue map, for the workspace doc's linkVenues key. */
  snapshotVenues(): Partial<Record<Exclude<LinkGroup, null>, VenueID>> {
    return Object.fromEntries(this.focusedVenues);
  }
}
