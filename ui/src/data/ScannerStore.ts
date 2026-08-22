import { ReactStore } from "./store";
import type {
  SnapshotMsg, DeltaMsg, ScannerRow, ScannerRankPayload, ScannerSession,
} from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isUnseen: boolean; isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null; filters: ScannerRankPayload["filters"] | null }
interface ScannerState { sessions: Partial<Record<ScannerSession, ScannerSessionView>> }
export interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null; filters: ScannerRankPayload["filters"] | null }

// Session-parameterized rank store. Rows arrive per session on the message `key`.
// New-hit flash + midnight-reset dedup are UI-authoritative: a per-session
// seen-set drives isNewHit/muted. A snapshot is a baseline (seed the seen-set,
// no flash); a delta is a refresh (flash symbols not yet seen). scanner.hit is an
// explicit force-flash for a symbol already in the current ranking.
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly known = new Map<ScannerSession, Set<string>>();
  private readonly unseen = new Map<ScannerSession, Set<string>>();
  private readonly revisions = new Map<ScannerSession, number>();
  private filterRevision = 0;
  private readonly hitListeners = new Set<(symbol: string) => void>();
  constructor() { super({ sessions: {} }); }

  onNewHit(cb: (symbol: string) => void): () => void {
    this.hitListeners.add(cb);
    return () => { this.hitListeners.delete(cb); };
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const session = (m.key ?? "premarket") as ScannerSession;
    if (m.topic === "scanner.hit") return; // rank payload owns baseline/unseen semantics
    const payload = m.payload as ScannerRankPayload;
    const { refreshedAt, rows, filters, baseline } = payload;
    const revision = payload.revision ?? 0;
    const current = this.getSnapshot().sessions[session];
    if (revision < this.filterRevision || (current && revision < (this.revisions.get(session) ?? 0))) return;
    const known = this.setFor(this.known, session);
    const unseen = this.setFor(this.unseen, session);
    if (m.kind === "snapshot" || baseline) { known.clear(); unseen.clear(); }
    // A delta against an empty seen-set is a session's first board (rollover,
    // fresh session start, or post-reset): seed it silently so the whole board
    // does not flash/chime at once. Genuinely-new symbols flash on later deltas.
    const isBaseline = m.kind === "snapshot" || baseline || known.size === 0;
    const newHits: string[] = [];
    const view: ScannerRowView[] = rows.map((row) => {
      if (!isBaseline && !known.has(row.symbol)) { unseen.add(row.symbol); newHits.push(row.symbol); }
      const isUnseen = unseen.has(row.symbol);
      return {
        ...row,
        volumeRatio: row.volumeRatio ?? null,
        shortInterest: row.shortInterest ?? null,
        shortInterestAsOf: row.shortInterestAsOf ?? null,
        isUnseen,
        isNewHit: isUnseen,
        muted: false,
      };
    });
    for (const row of rows) known.add(row.symbol);
    this.revisions.set(session, revision);
    this.setSession(session, { rows: view, refreshedAt, filters });
    // fired after the map (not inside it) so the row-view build stays a pure transform
    for (const symbol of newHits) {
      for (const cb of this.hitListeners) {
        try { cb(symbol); } catch { /* a listener must never break scanner ingestion */ }
      }
    }
  }

  view(session: ScannerSession): ScannerSessionView {
    return this.getSnapshot().sessions[session] ?? { rows: [], refreshedAt: null, filters: null };
  }

  // The session view with the freshest refreshedAt — the "live" board the
  // panels follow. Null session until any data arrives.
  currentView(): CurrentScannerView {
    const sessions = this.getSnapshot().sessions;
    let best: ScannerSession | null = null;
    let bestT = -Infinity;
    for (const key of Object.keys(sessions) as ScannerSession[]) {
      const v = sessions[key];
      if (!v?.refreshedAt) continue;
      const t = Date.parse(v.refreshedAt);
      const ms = Number.isNaN(t) ? -Infinity : t;
      if (ms > bestT) { bestT = ms; best = key; }
    }
    if (!best) return { session: null, rows: [], refreshedAt: null, filters: null };
    const v = sessions[best]!;
    return { session: best, rows: v.rows, refreshedAt: v.refreshedAt, filters: v.filters };
  }

  setFilters(filters: ScannerRankPayload["filters"], revision: number): void {
    if (revision < this.filterRevision) return;
    this.filterRevision = revision;
    const sessions = { ...this.getSnapshot().sessions };
    for (const session of Object.keys(sessions) as ScannerSession[]) {
      const view = sessions[session];
      if (view && revision >= (this.revisions.get(session) ?? 0)) {
        this.revisions.set(session, revision);
        sessions[session] = { ...view, filters };
      }
    }
    this.set({ sessions });
  }

  resetSeen(session?: ScannerSession): void {
    if (session) { this.setFor(this.known, session).clear(); this.setFor(this.unseen, session).clear(); }
    else { this.known.clear(); this.unseen.clear(); }
  }

  markSeen(session: ScannerSession, symbol: string): void {
    this.setFor(this.unseen, session).delete(symbol);
    const cur = this.getSnapshot().sessions[session]; if (!cur) return;
    this.setSession(session, { ...cur, rows: cur.rows.map((r) => r.symbol === symbol ? { ...r, isUnseen: false, isNewHit: false, muted: false } : r) });
  }

  private setFor(map: Map<ScannerSession, Set<string>>, session: ScannerSession): Set<string> {
    let s = map.get(session);
    if (!s) { s = new Set(); map.set(session, s); }
    return s;
  }

  private setSession(session: ScannerSession, view: ScannerSessionView): void {
    this.set({ sessions: { ...this.getSnapshot().sessions, [session]: view } });
  }
}
