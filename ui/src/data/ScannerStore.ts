import { ReactStore } from "./store";
import type {
  SnapshotMsg, DeltaMsg, ScannerRow, ScannerRankPayload, ScanHitPayload, ScannerSession,
} from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null }
interface ScannerState { sessions: Partial<Record<ScannerSession, ScannerSessionView>> }
export interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null }

// Session-parameterized rank store. Rows arrive per session on the message `key`.
// New-hit flash + midnight-reset dedup are UI-authoritative: a per-session
// seen-set drives isNewHit/muted. A snapshot is a baseline (seed the seen-set,
// no flash); a delta is a refresh (flash symbols not yet seen). scanner.hit is an
// explicit force-flash for a symbol already in the current ranking.
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly seen = new Map<ScannerSession, Set<string>>();
  private readonly hitListeners = new Set<(symbol: string) => void>();
  constructor() { super({ sessions: {} }); }

  onNewHit(cb: (symbol: string) => void): () => void {
    this.hitListeners.add(cb);
    return () => { this.hitListeners.delete(cb); };
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const session = (m.key ?? "premarket") as ScannerSession;
    if (m.topic === "scanner.hit") { this.applyHit(session, m.payload as ScanHitPayload); return; }
    const { refreshedAt, rows } = m.payload as ScannerRankPayload;
    const seen = this.seenFor(session);
    if (m.kind === "snapshot") seen.clear(); // a (re)snapshot is a fresh baseline
    // A delta against an empty seen-set is a session's first board (rollover,
    // fresh session start, or post-reset): seed it silently so the whole board
    // does not flash/chime at once. Genuinely-new symbols flash on later deltas.
    const isBaseline = m.kind === "snapshot" || seen.size === 0;
    const newHits: string[] = [];
    const view: ScannerRowView[] = rows.map((row) => {
      const isNewHit = !isBaseline && !seen.has(row.symbol);
      const muted = !isBaseline && seen.has(row.symbol);
      if (isNewHit) newHits.push(row.symbol);
      return { ...row, isNewHit, muted };
    });
    for (const row of rows) seen.add(row.symbol);
    this.setSession(session, { rows: view, refreshedAt });
    // fired after the map (not inside it) so the row-view build stays a pure transform
    for (const symbol of newHits) {
      for (const cb of this.hitListeners) {
        try { cb(symbol); } catch { /* a listener must never break scanner ingestion */ }
      }
    }
  }

  view(session: ScannerSession): ScannerSessionView {
    return this.getSnapshot().sessions[session] ?? { rows: [], refreshedAt: null };
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
    if (!best) return { session: null, rows: [], refreshedAt: null };
    const v = sessions[best]!;
    return { session: best, rows: v.rows, refreshedAt: v.refreshedAt };
  }

  resetSeen(session?: ScannerSession): void {
    if (session) this.seenFor(session).clear();
    else this.seen.clear();
  }

  private applyHit(session: ScannerSession, hit: ScanHitPayload): void {
    for (const cb of this.hitListeners) {
      try { cb(hit.symbol); } catch { /* a listener must never break scanner ingestion */ }
    }
    this.seenFor(session).add(hit.symbol);
    const cur = this.getSnapshot().sessions[session];
    if (!cur) return;
    const rows = cur.rows.map((row) =>
      row.symbol === hit.symbol ? { ...row, isNewHit: true, muted: false } : row);
    this.setSession(session, { rows, refreshedAt: cur.refreshedAt });
  }

  private seenFor(session: ScannerSession): Set<string> {
    let s = this.seen.get(session);
    if (!s) { s = new Set(); this.seen.set(session, s); }
    return s;
  }

  private setSession(session: ScannerSession, view: ScannerSessionView): void {
    this.set({ sessions: { ...this.getSnapshot().sessions, [session]: view } });
  }
}
