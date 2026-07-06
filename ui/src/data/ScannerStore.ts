import { ReactStore } from "./store";
import type {
  SnapshotMsg, DeltaMsg, ScannerRow, ScannerRankPayload, ScanHitPayload, ScannerSession,
} from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null }
interface ScannerState { sessions: Partial<Record<ScannerSession, ScannerSessionView>> }

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
    if (m.kind === "snapshot") seen.clear(); // a (re)snapshot is a fresh baseline: no flash, no stale mute
    const view: ScannerRowView[] = rows.map((row) => {
      const isNewHit = m.kind === "delta" && !seen.has(row.symbol);
      const muted = m.kind === "delta" && seen.has(row.symbol);
      if (isNewHit) for (const cb of this.hitListeners) cb(row.symbol);
      return { ...row, isNewHit, muted };
    });
    for (const row of rows) seen.add(row.symbol);
    this.setSession(session, { rows: view, refreshedAt });
  }

  view(session: ScannerSession): ScannerSessionView {
    return this.getSnapshot().sessions[session] ?? { rows: [], refreshedAt: null };
  }

  resetSeen(session?: ScannerSession): void {
    if (session) this.seenFor(session).clear();
    else this.seen.clear();
  }

  private applyHit(session: ScannerSession, hit: ScanHitPayload): void {
    for (const cb of this.hitListeners) cb(hit.symbol);
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
