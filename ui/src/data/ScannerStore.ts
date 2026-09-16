import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, ScannerRankPayload, ScannerRow, ScannerSession } from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isUnseen: boolean; isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null; filters: ScannerRankPayload["filters"] | null; warmingCount: number }
interface ScannerState { sessions: Partial<Record<ScannerSession, ScannerSessionView>> }
export interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null; filters: ScannerRankPayload["filters"] | null; warmingCount: number }

// Alert revisions are engine-authoritative. Unread state is UI-only and never
// changes the revision watermark, so repeated crossings remain independent of
// row selection or when the user last opened a session.
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly lastRevision = new Map<string, number>();
  private readonly unseen = new Map<ScannerSession, Set<string>>();
  private readonly hitListeners = new Set<(symbol: string) => void>();
  constructor() { super({ sessions: {} }); }

  onNewHit(cb: (symbol: string) => void): () => void {
    this.hitListeners.add(cb);
    return () => { this.hitListeners.delete(cb); };
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic === "scanner.hit") return;
    const session = (m.key ?? "premarket") as ScannerSession;
    const { refreshedAt, rows, filters, baseline, warmingCount } = m.payload as ScannerRankPayload;
    const unseen = this.setFor(this.unseen, session);
    const seed = m.kind === "snapshot" || baseline === true;
    const newAlerts: string[] = [];
    const view = rows.map((row) => {
      const revision = row.alertSeq ?? 0;
      const previous = this.lastRevision.get(row.symbol) ?? 0;
      if (seed) {
        this.lastRevision.set(row.symbol, revision);
      } else if (revision > previous) {
        this.lastRevision.set(row.symbol, revision);
        unseen.add(row.symbol);
        newAlerts.push(row.symbol);
      }
      return {
        ...row,
        changeStatus: row.changeStatus ?? (row.changePct == null ? "unavailable" : "ready"),
        relativeVolume: row.relativeVolume ?? null,
        shortInterest: row.shortInterest ?? null,
        shortInterestAsOf: row.shortInterestAsOf ?? null,
        isUnseen: unseen.has(row.symbol),
        isNewHit: unseen.has(row.symbol),
        muted: false,
      };
    });
    this.setSession(session, { rows: view, refreshedAt, filters: filters ?? null, warmingCount: warmingCount ?? 0 });
    for (const symbol of newAlerts) {
      for (const cb of this.hitListeners) {
        try { cb(symbol); } catch { /* notification wiring must not break ingestion */ }
      }
    }
  }

  view(session: ScannerSession): ScannerSessionView {
    return this.getSnapshot().sessions[session] ?? { rows: [], refreshedAt: null, filters: null, warmingCount: 0 };
  }

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
    if (!best) return { session: null, rows: [], refreshedAt: null, filters: null, warmingCount: 0 };
    const v = sessions[best]!;
    return { session: best, rows: v.rows, refreshedAt: v.refreshedAt, filters: v.filters, warmingCount: v.warmingCount };
  }

  // Compatibility shim for old panel callers. Alert watermarks are deliberately
  // not reset; only unread highlighting is a UI concern.
  resetSeen(session?: ScannerSession): void {
    if (session) this.setFor(this.unseen, session).clear();
    else this.unseen.clear();
    const sessions = this.getSnapshot().sessions;
    const next = { ...sessions };
    for (const [key, view] of Object.entries(next) as [ScannerSession, ScannerSessionView][]) {
      if (session && key !== session) continue;
      next[key] = { ...view, rows: view.rows.map((r) => ({ ...r, isUnseen: false, isNewHit: false })) };
    }
    this.set({ sessions: next });
  }

  markSeen(session: ScannerSession, symbol: string): void {
    this.setFor(this.unseen, session).delete(symbol);
    const cur = this.getSnapshot().sessions[session];
    if (!cur) return;
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
