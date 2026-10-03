import { ReactStore } from "./store";
import type {
  SnapshotMsg, DeltaMsg, ScannerRow, ScannerRankPayload, ScannerSession,
} from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isUnseen: boolean; isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null; discoveryAt: string | null; status: ScannerRankPayload["status"] | null; filters: ScannerRankPayload["filters"] | null }
interface ScannerState { sessions: Record<string, ScannerSessionView> }
export interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null; discoveryAt: string | null; status: ScannerRankPayload["status"] | null; filters: ScannerRankPayload["filters"] | null }
const LEGACY_SCANNER = "legacy";

// Rows are keyed by stable workspace/panel identity and session. Legacy
// servers without scannerId continue to use one compatibility board.
// New-hit flash + midnight-reset dedup are UI-authoritative: a per-board/session
// seen-set drives isNewHit/muted. A snapshot is a baseline (seed the seen-set,
// no flash); a delta is a refresh (flash symbols not yet seen). scanner.hit is an
// explicit force-flash for a symbol already in the current ranking.
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly known = new Map<string, Set<string>>();
  private readonly unseen = new Map<string, Set<string>>();
  private readonly emittedHits = new Map<string, Set<string>>();
  private readonly hitListeners = new Map<string, Set<(symbol: string) => void>>();
  constructor() { super({ sessions: {} }); }

  onNewHit(cb: (symbol: string) => void, scannerId = LEGACY_SCANNER): () => void {
    let listeners = this.hitListeners.get(scannerId);
    if (!listeners) { listeners = new Set(); this.hitListeners.set(scannerId, listeners); }
    listeners.add(cb);
    return () => { listeners!.delete(cb); if (listeners!.size === 0) this.hitListeners.delete(scannerId); };
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic === "scanner.hit") {
      const hit = m.payload as { scannerId?: string; session?: string; symbol: string };
      const scannerId = hit.scannerId || LEGACY_SCANNER;
      const key = `${scannerId}/${hit.session || m.key?.split("/").pop() || "premarket"}`;
      const emitted = this.emittedHits.get(key);
      if (emitted?.delete(hit.symbol)) {
        if (emitted.size === 0) this.emittedHits.delete(key);
        return; // rank delta already raised the panel's sound and unseen state
      }
      this.setFor(this.unseen, key).add(hit.symbol);
      const current = this.getSnapshot().sessions[key];
      if (current) this.setSession(key, { ...current, rows: current.rows.map((row) => row.symbol === hit.symbol ? { ...row, isUnseen: true, isNewHit: true, muted: false } : row) });
      for (const cb of this.hitListeners.get(scannerId) ?? []) {
        try { cb(hit.symbol); } catch { /* a listener must never break scanner ingestion */ }
      }
      return;
    }
    const payload = m.payload as ScannerRankPayload;
    const scannerId = payload.scannerId || LEGACY_SCANNER;
    if (payload.status === "deleted") {
      const prefix = `${scannerId}/`;
      const sessions = Object.fromEntries(Object.entries(this.getSnapshot().sessions).filter(([key]) => !key.startsWith(prefix)));
      for (const key of this.known.keys()) if (key.startsWith(prefix)) this.known.delete(key);
      for (const key of this.unseen.keys()) if (key.startsWith(prefix)) this.unseen.delete(key);
      for (const key of this.emittedHits.keys()) if (key.startsWith(prefix)) this.emittedHits.delete(key);
      this.set({ sessions });
      return;
    }
    const session = (payload.session || m.key?.split("/").at(-1) || "premarket") as ScannerSession;
    const { refreshedAt, discoveryAt, status, rows, filters, baseline } = payload;
    const boardSession = `${scannerId}/${session}`;
    const known = this.setFor(this.known, boardSession);
    const unseen = this.setFor(this.unseen, boardSession);
    if (m.kind === "snapshot" || baseline) { known.clear(); unseen.clear(); }
    // A delta against an empty seen-set is a session's first board (rollover,
    // fresh session start, or post-reset): seed it silently so the whole board
    // does not flash/chime at once. Genuinely-new symbols flash on later deltas.
    const isBaseline = m.kind === "snapshot" || baseline || known.size === 0;
    const newHits: string[] = [];
    const view: ScannerRowView[] = rows.map((row) => {
      const isNewHit = !isBaseline && !known.has(row.symbol);
      if (isNewHit) { unseen.add(row.symbol); newHits.push(row.symbol); }
      const isUnseen = unseen.has(row.symbol);
      return {
        ...row,
        volume: row.volume ?? null,
        sessionVolume: row.sessionVolume ?? null,
        turnover: row.turnover ?? null,
        relativeVolume: row.relativeVolume ?? null,
        shortInterest: row.shortInterest ?? null,
        shortInterestAsOf: row.shortInterestAsOf ?? null,
        isUnseen,
        isNewHit,
        muted: !isBaseline && !isNewHit,
      };
    });
    for (const row of rows) known.add(row.symbol);
    this.setSession(boardSession, { rows: view, refreshedAt, discoveryAt, status, filters });
    // fired after the map (not inside it) so the row-view build stays a pure transform
    if (newHits.length) this.emittedHits.set(boardSession, new Set(newHits));
    for (const symbol of newHits) {
      for (const cb of this.hitListeners.get(scannerId) ?? []) {
        try { cb(symbol); } catch { /* a listener must never break scanner ingestion */ }
      }
    }
  }

  view(session: ScannerSession, scannerId = LEGACY_SCANNER): ScannerSessionView {
    return this.getSnapshot().sessions[`${scannerId}/${session}`] ?? EMPTY_VIEW;
  }

  // The session view with the freshest refreshedAt — the "live" board the
  // panels follow. Null session until any data arrives.
  currentView(scannerId = LEGACY_SCANNER): CurrentScannerView {
    const sessions = this.getSnapshot().sessions;
    const prefix = `${scannerId}/`;
    let best: ScannerSession | null = null;
    let bestT = -Infinity;
    for (const [key, v] of Object.entries(sessions)) {
      if (!key.startsWith(prefix)) continue;
      if (!v || (!v.refreshedAt && v.rows.length === 0 && v.status !== "paused" && v.status !== "delayed")) continue;
      const t = v.refreshedAt ? Date.parse(v.refreshedAt) : -Infinity;
      const ms = Number.isNaN(t) ? -Infinity : t;
      if (best === null || ms > bestT) { bestT = ms; best = key.slice(prefix.length) as ScannerSession; }
    }
    if (!best) return { session: null, rows: [], refreshedAt: null, discoveryAt: null, status: null, filters: null };
    const v = sessions[`${scannerId}/${best}`]!;
    return { session: best, rows: v.rows, refreshedAt: v.refreshedAt, discoveryAt: v.discoveryAt, status: v.status, filters: v.filters };
  }

  resetSeen(session?: ScannerSession, scannerId = LEGACY_SCANNER): void {
    if (session) { this.setFor(this.known, `${scannerId}/${session}`).clear(); this.setFor(this.unseen, `${scannerId}/${session}`).clear(); }
    else if (scannerId !== LEGACY_SCANNER) {
      const prefix = `${scannerId}/`;
      for (const key of this.known.keys()) if (key.startsWith(prefix)) this.known.delete(key);
      for (const key of this.unseen.keys()) if (key.startsWith(prefix)) this.unseen.delete(key);
    } else { this.known.clear(); this.unseen.clear(); }
  }

  markSeen(session: ScannerSession, symbol: string, scannerId = LEGACY_SCANNER): void {
    this.setFor(this.unseen, `${scannerId}/${session}`).delete(symbol);
    const cur = this.getSnapshot().sessions[`${scannerId}/${session}`]; if (!cur) return;
    this.setSession(`${scannerId}/${session}`, { ...cur, rows: cur.rows.map((r) => r.symbol === symbol ? { ...r, isUnseen: false, isNewHit: false, muted: false } : r) });
  }

  private setFor(map: Map<string, Set<string>>, key: string): Set<string> {
    let s = map.get(key);
    if (!s) { s = new Set(); map.set(key, s); }
    return s;
  }

  private setSession(key: string, view: ScannerSessionView): void {
    this.set({ sessions: { ...this.getSnapshot().sessions, [key]: view } });
  }
}

const EMPTY_VIEW: ScannerSessionView = { rows: [], refreshedAt: null, discoveryAt: null, status: null, filters: null };
