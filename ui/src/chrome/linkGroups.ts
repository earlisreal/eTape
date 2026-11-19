import type { AckMsg, ExecStatus, VenueID, VenueStatus } from "../wire/contract";
import { cryptoBrokerExchange, cryptoExchange, type CryptoExchange } from "./cryptoScanner";
import { CrosshairSync } from "./crosshairSync";

export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
type VenueMarket = "stocks" | CryptoExchange;
type VenueSelections = Partial<Record<VenueMarket, VenueID>> & { legacy?: VenueID; switchedFrom?: VenueID };
export type SavedLinkVenues = Partial<Record<Exclude<LinkGroup, null>, VenueID | VenueSelections>>;
export interface LinkMsg { group: LinkGroup; symbol?: string; venue?: VenueID; venueMarket?: VenueMarket }

export function venueMarket(symbol: string | undefined): VenueMarket | undefined {
  return symbol?.startsWith("PERP.") ? cryptoExchange(symbol.split(".")[1]) : "stocks";
}

export function venueSupportsMarket(venue: VenueStatus, market: VenueMarket | undefined): boolean {
  if (market === "stocks") return ["alpaca", "tradezero", "moomoo", "sim"].includes(venue.broker);
  return market !== undefined && cryptoBrokerExchange(venue.broker) === market;
}

function requiresLiveConfirmation(status: ExecStatus | null | undefined, current: VenueID | undefined, next: VenueID): boolean {
  if (!current || current === next || !status) return false;
  const env = (id: VenueID) => status.venues.find(v => v.venue === id)?.env?.toLowerCase();
  return env(current) === "paper" && env(next) === "live";
}
export interface FocusTiming { symbol: string; startedAt: number; sequence: number }

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
  readonly crosshairSync = new CrosshairSync();
  private readonly focused = new Map<Exclude<LinkGroup, null>, string>();
  private readonly focusedVenues = new Map<Exclude<LinkGroup, null>, VenueID | VenueSelections>();
  private readonly previousVenues = new Map<Exclude<LinkGroup, null>, VenueID>();
  private readonly focusTimings = new Map<Exclude<LinkGroup, null>, FocusTiming>();
  private focusTimingSequence = 0;
  private readonly subs = new Set<() => void>();

  constructor(
    private readonly bus: LinkBus,
    private readonly onEcho: (group: Exclude<LinkGroup, null>, symbol: string) => Promise<AckMsg> | void,
    private readonly getExecStatus?: () => ExecStatus | null,
  ) {
    this.bus.onMessage((msg) => {
      if (!msg.group) return;
      if (msg.symbol !== undefined) this.setLocal(msg.group, msg.symbol);
      if (msg.venue !== undefined) this.setLocalVenue(msg.group, msg.venue, msg.venueMarket ?? venueMarket(this.symbolFor(msg.group)));
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
    const timing: FocusTiming = { symbol, startedAt: performance.now(), sequence: ++this.focusTimingSequence };
    this.focusTimings.set(group, timing);
    const ackP = this.onEcho(group, symbol);
    const ack = ackP ? await ackP : { status: "accepted" as const };
    if (ack.status !== "accepted") {
      if (this.focusTimings.get(group)?.sequence === timing.sequence) this.focusTimings.delete(group);
      return { ok: false, reason: ack.reason ?? "symbol rejected" };
    }
    this.setLocal(group, symbol);
    this.bus.post({ group, symbol });
    return { ok: true };
  }

  private setLocal(group: Exclude<LinkGroup, null>, symbol: string): void {
    if (venueMarket(this.symbolFor(group)) !== venueMarket(symbol)) {
      const status = this.getExecStatus?.();
      const from = venueMarket(this.symbolFor(group));
      // Remember the prior selection even if the engine snapshot is still loading.
      const previous = status === null && from ? this.savedVenueFor(group, undefined, from) : this.venueFor(group, status);
      if (previous) this.focusedVenues.set(group, { ...this.selections(group), switchedFrom: previous });
    }
    this.focused.set(group, symbol);
    this.subs.forEach((cb) => cb());
  }

  symbolFor(group: LinkGroup): string | undefined {
    return group ? this.focused.get(group) : undefined;
  }

  // Temporary cold-chart diagnostic. ChartPanel reads this after group focus
  // changes so timing includes symbol validation and demand setup.
  focusTimingFor(group: LinkGroup, symbol: string): FocusTiming | undefined {
    if (!group) return undefined;
    const timing = this.focusTimings.get(group);
    return timing?.symbol === symbol ? timing : undefined;
  }

  // Venue focus is UI-only state — unlike symbol focus it does NOT echo to the
  // engine (the engine has no per-group venue concept). It publishes cross-window
  // and notifies subscribers so grouped panels re-render.
  focusVenue(group: Exclude<LinkGroup, null>, venue: VenueID): void {
    const market = venueMarket(this.symbolFor(group));
    if (!market) return;
    this.setLocalVenue(group, venue, market);
    this.bus.post({ group, venue, venueMarket: market });
  }

  private selections(group: Exclude<LinkGroup, null>): VenueSelections {
    const saved = this.focusedVenues.get(group);
    return typeof saved === "string" ? { legacy: saved } : saved ?? {};
  }

  private savedVenueFor(group: Exclude<LinkGroup, null>, status: ExecStatus | null | undefined, market: VenueMarket): VenueID | undefined {
    const saved = this.selections(group);
    const candidate = saved[market] ?? saved.legacy;
    if (status === undefined) return candidate;
    return status?.venues.find(venue => venue.venue === candidate && venueSupportsMarket(venue, market))?.venue;
  }

  venueFor(group: LinkGroup, status = this.getExecStatus?.()): VenueID | undefined {
    const market = venueMarket(this.symbolFor(group));
    if (!group || !market) return undefined;
    const candidate = this.savedVenueFor(group, status, market);
    if (market !== "stocks" && candidate && requiresLiveConfirmation(status, this.selections(group).switchedFrom, candidate)) {
      return undefined;
    }
    return candidate;
  }

  awaitingLiveConfirmation(group: LinkGroup, status: ExecStatus | null): boolean {
    const market = venueMarket(this.symbolFor(group));
    if (!group || !market || market === "stocks") return false;
    const saved = this.savedVenueFor(group, status, market);
    return !!saved && requiresLiveConfirmation(status, this.selections(group).switchedFrom, saved);
  }

  needsLiveConfirmation(group: LinkGroup, venue: VenueID, status = this.getExecStatus?.()): boolean {
    if (!group) return false;
    const current = this.venueFor(group, status) ?? this.selections(group).switchedFrom;
    return requiresLiveConfirmation(status, current, venue);
  }

  matchesOrderContext(group: LinkGroup, symbol: string, venue: VenueID, status = this.getExecStatus?.()): boolean {
    const focused = this.symbolFor(group);
    return !!venue && venue === this.venueFor(group, status) && (!focused || symbol === focused);
  }

  previousVenueFor(group: LinkGroup): VenueID | undefined {
    return group ? this.previousVenues.get(group) : undefined;
  }

  private setLocalVenue(group: Exclude<LinkGroup, null>, venue: VenueID, market: VenueMarket | undefined): void {
    if (!market) return;
    const selections = this.selections(group);
    const previous = this.savedVenueFor(group, this.getExecStatus?.(), market);
    if (previous && previous !== venue) this.previousVenues.set(group, previous);
    const next = { ...selections, [market]: venue };
    if (market === venueMarket(this.symbolFor(group))) delete next.switchedFrom;
    this.focusedVenues.set(group, next);
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
  hydrateVenues(map: SavedLinkVenues): void {
    this.focusedVenues.clear();
    this.previousVenues.clear();
    for (const [group, venue] of Object.entries(map) as [Exclude<LinkGroup, null>, VenueID | VenueSelections][]) {
      if (venue) this.focusedVenues.set(group, venue);
    }
  }

  /** Plain-object snapshot of the focused-venue map, for the workspace doc's linkVenues key. */
  snapshotVenues(): SavedLinkVenues {
    return Object.fromEntries(this.focusedVenues);
  }
}
