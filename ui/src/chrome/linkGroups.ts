import type { AckMsg, ExecStatus, VenueID, VenueStatus } from "../wire/contract";
import { cryptoBrokerExchange, cryptoExchange, type CryptoExchange } from "./cryptoScanner";
import { CrosshairSync } from "./crosshairSync";

export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
type Group = Exclude<LinkGroup, null>;
export type VenueMarket = "stocks" | CryptoExchange;
type LinkMarket = VenueMarket | "crypto";
type MarketMemory = {
  emptyMarket?: LinkMarket;
  lastCrypto?: CryptoExchange;
  symbols?: Partial<Record<VenueMarket, string>>;
};
export type SavedLinkMarkets = Partial<Record<Group, MarketMemory>>;
type VenueSelections = Partial<Record<VenueMarket, VenueID>> & { legacy?: VenueID; switchedFrom?: VenueID };
export type SavedLinkVenues = Partial<Record<Exclude<LinkGroup, null>, VenueID | VenueSelections>>;
export interface LinkMsg { group: LinkGroup; symbol?: string; market?: LinkMarket; venue?: VenueID; venueMarket?: VenueMarket }

export function venueMarket(symbol: string | undefined): VenueMarket | undefined {
  return symbol?.startsWith("PERP.") ? cryptoExchange(symbol.split(".")[1]) : "stocks";
}

export function savedLinkMarket(symbol: string | undefined, emptyMarket: unknown): LinkMarket | undefined {
  return symbol ? venueMarket(symbol) : emptyMarket === "crypto" ? "crypto" : cryptoExchange(emptyMarket) ?? "stocks";
}

export function venueSupportsMarket(venue: VenueStatus, market: LinkMarket | undefined): boolean {
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
  private readonly markets = new Map<Group, MarketMemory>();
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
    this.bus.onMessage(msg => this.apply(msg));
  }

  private apply(msg: LinkMsg, keepTiming = false): void {
    const group = msg.group;
    if (!group) return;
    if (msg.symbol !== undefined) {
      if (!keepTiming) this.focusTimings.delete(group);
      this.setLocal(group, msg.symbol, msg.market);
    }
    if (msg.venue !== undefined) this.setLocalVenue(group, msg.venue, msg.venueMarket ?? this.marketFor(group), msg.symbol === undefined);
    this.subs.forEach(cb => cb());
  }

  focus(group: Exclude<LinkGroup, null>, symbol: string): void {
    const message = { group, symbol };
    this.apply(message);
    this.bus.post(message);
    this.onEcho(group, symbol);
  }

  private rememberedSymbol(group: Group, market: LinkMarket): string {
    const remembered = market === "crypto" ? undefined : this.markets.get(group)?.symbols?.[market];
    return remembered && venueMarket(remembered) === market ? remembered : "";
  }

  focusMarket(group: Group, market: LinkMarket): void {
    if (market === this.marketFor(group)) return;
    const symbol = this.rememberedSymbol(group, market);
    const message = { group, symbol, market };
    this.apply(message);
    this.bus.post(message);
    if (symbol) this.onEcho(group, symbol);
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
    if (this.focusTimings.get(group) !== timing) return { ok: false, reason: "Symbol selection superseded" };
    if (ack.status !== "accepted") {
      this.focusTimings.delete(group);
      return { ok: false, reason: ack.reason ?? "symbol rejected" };
    }
    const message = { group, symbol };
    this.apply(message, true);
    this.bus.post(message);
    return { ok: true };
  }

  private setLocal(group: Group, symbol: string, emptyMarket?: LinkMarket): void {
    const from = this.marketFor(group);
    const market = savedLinkMarket(symbol, emptyMarket);
    const oldSymbol = this.symbolFor(group);
    const memory = { ...this.markets.get(group) };
    if (from !== market) {
      const status = this.getExecStatus?.();
      // Remember the prior selection even if the engine snapshot is still loading.
      const previous = status === null && from ? this.savedVenueFor(group, undefined, from) : this.venueFor(group, status);
      if (previous) {
        this.focusedVenues.set(group, { ...this.selections(group), switchedFrom: previous });
        this.previousVenues.set(group, previous);
      }
      if (from && from !== "crypto" && oldSymbol) memory.symbols = { ...memory.symbols, [from]: oldSymbol };
    }
    const latestCrypto = cryptoExchange(market) ?? cryptoExchange(from);
    if (latestCrypto) memory.lastCrypto = latestCrypto;
    if (symbol) {
      this.focused.set(group, symbol);
      delete memory.emptyMarket;
    } else {
      this.focused.delete(group);
      memory.emptyMarket = market ?? "stocks";
    }
    this.markets.set(group, memory);
  }

  symbolFor(group: LinkGroup): string | undefined {
    // An explicitly cleared focus suppresses the panel's legacy symbol fallback.
    return group ? this.focused.get(group) ?? (this.markets.get(group)?.emptyMarket ? "" : undefined) : undefined;
  }

  marketFor(group: LinkGroup): LinkMarket | undefined {
    return group ? savedLinkMarket(this.focused.get(group), this.markets.get(group)?.emptyMarket) : undefined;
  }

  lastCryptoFor(group: Group): CryptoExchange | undefined {
    return cryptoExchange(this.marketFor(group)) ?? cryptoExchange(this.markets.get(group)?.lastCrypto);
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
  focusVenue(group: Group, venue: VenueID, market = this.marketFor(group)): void {
    if (!market || market === "crypto") return;
    const symbol = market === this.marketFor(group) ? undefined : this.rememberedSymbol(group, market);
    const message: LinkMsg = { group, venue, venueMarket: market, ...(symbol !== undefined && { symbol, market }) };
    this.apply(message);
    this.bus.post(message);
    if (symbol) this.onEcho(group, symbol);
  }

  private selections(group: Exclude<LinkGroup, null>): VenueSelections {
    const saved = this.focusedVenues.get(group);
    return typeof saved === "string" ? { legacy: saved } : saved ?? {};
  }

  private savedVenueFor(group: Group, status: ExecStatus | null | undefined, market: LinkMarket): VenueID | undefined {
    if (market === "crypto") return undefined;
    const saved = this.selections(group);
    const candidate = saved[market] ?? saved.legacy;
    if (status === undefined) return candidate;
    return status?.venues.find(venue => venue.venue === candidate && venueSupportsMarket(venue, market))?.venue;
  }

  venueFor(group: LinkGroup, status = this.getExecStatus?.()): VenueID | undefined {
    const market = this.marketFor(group);
    if (!group || !market) return undefined;
    const candidate = this.savedVenueFor(group, status, market);
    if (market !== "stocks" && candidate && requiresLiveConfirmation(status, this.selections(group).switchedFrom, candidate)) {
      return undefined;
    }
    return candidate;
  }

  awaitingLiveConfirmation(group: LinkGroup, status: ExecStatus | null): boolean {
    const market = this.marketFor(group);
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
    return !!venue && venue === this.venueFor(group, status) && (focused === undefined || symbol === focused);
  }

  previousVenueFor(group: LinkGroup): VenueID | undefined {
    return group ? this.previousVenues.get(group) : undefined;
  }

  private setLocalVenue(group: Group, venue: VenueID, market: LinkMarket | undefined, recordPrevious: boolean): void {
    if (!market || market === "crypto") return;
    const selections = this.selections(group);
    const previous = this.savedVenueFor(group, this.getExecStatus?.(), market);
    if (recordPrevious && previous && previous !== venue) this.previousVenues.set(group, previous);
    const next = { ...selections, [market]: venue };
    if (market === this.marketFor(group)) delete next.switchedFrom;
    this.focusedVenues.set(group, next);
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
  hydrate(map: Partial<Record<Group, string>>, markets: SavedLinkMarkets = {}): void {
    this.focused.clear();
    this.markets.clear();
    this.focusTimings.clear();
    for (const [group, memory] of Object.entries(markets) as [Group, MarketMemory][]) {
      if (memory) this.markets.set(group, memory);
    }
    for (const [group, symbol] of Object.entries(map) as [Exclude<LinkGroup, null>, string][]) {
      if (symbol) this.focused.set(group, symbol);
    }
  }

  snapshotMarkets(): SavedLinkMarkets {
    return Object.fromEntries(this.markets);
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
