// Diagnostic-only performance instrumentation (Task 0 of the UI perf plan).
// Purpose: measure the symbol-scoped-tick root cause (single global ring +
// dirty counter → every tape/ladder panel repaints and rescans on every
// tick, regardless of the displayed symbol) before Tasks 1-4 fix it, then
// re-measure after. This is a temporary measurement tool, not a shipped
// feature — no persistence, no export, no charting.
//
// Hard requirement: zero overhead when disabled. `enabled` defaults to
// false, and every recording method's very first line is `if (!this.enabled)
// return;` — no allocation, no Map writes, no clock reads happen before that
// check. Callers (Scheduler, WsClient, registry, TapePanel) call these
// methods unconditionally on the hot path; the no-op cost is a single
// boolean field read.

/** Per-surface paint-duration stats: last sample, running max, and a smoothed EWMA trend. */
export interface PaintStat {
  last: number;
  max: number;
  ewma: number;
}

/** Per-surface buildTapeRows scan-length stats (temporary — Tasks 1-4 remove the need for this). */
export interface ScanStat {
  last: number;
  max: number;
}

export interface PerfSnapshot {
  enabled: boolean;
  paint: Record<string, PaintStat>;
  scan: Record<string, ScanStat>;
  frame: { intervalMs: number | null; droppedFrames: number };
  wsMsgsPerSec: number;
  ticksPerSec: number;
}

const EWMA_ALPHA = 0.2;
const FRAME_BUDGET_MS = 16.7;
const DROPPED_FRAME_THRESHOLD_MS = FRAME_BUDGET_MS * 1.5;
const ROLLING_WINDOW_MS = 1000;

const EMPTY_SNAPSHOT: PerfSnapshot = {
  enabled: false,
  paint: {},
  scan: {},
  frame: { intervalMs: null, droppedFrames: 0 },
  wsMsgsPerSec: 0,
  ticksPerSec: 0,
};

export class PerfMonitor {
  enabled = false;

  private readonly paintStats = new Map<string, PaintStat>();
  private readonly scanStats = new Map<string, ScanStat>();

  private lastFrameTime: number | null = null;
  private lastFrameInterval: number | null = null;
  private droppedFrames = 0;

  // Rolling one-second windows for message/tick throughput. Rolled lazily —
  // on the next record() past the window boundary, or on snapshot() itself
  // (so a caller polling on an interval sees the rate decay to zero during
  // an idle period, not just on the next message).
  private msgWindowStart = 0;
  private msgWindowCount = 0;
  private msgsPerSec = 0;
  private tickWindowStart = 0;
  private tickWindowCount = 0;
  private ticksPerSec = 0;

  constructor(private readonly now: () => number = () => performance.now()) {}

  enable(): void {
    // Start every enabled session with a clean slate — stale numbers from a
    // previous session (or from before the toggle) would be misleading.
    this.reset();
    this.enabled = true;
  }

  disable(): void {
    this.enabled = false;
  }

  private reset(): void {
    this.paintStats.clear();
    this.scanStats.clear();
    this.lastFrameTime = null;
    this.lastFrameInterval = null;
    this.droppedFrames = 0;
    this.msgWindowStart = this.now();
    this.msgWindowCount = 0;
    this.msgsPerSec = 0;
    this.tickWindowStart = this.msgWindowStart;
    this.tickWindowCount = 0;
    this.ticksPerSec = 0;
  }

  /** Record how long one surface's paint() call took, in ms. */
  recordPaint(id: string, durationMs: number): void {
    if (!this.enabled) return;
    const existing = this.paintStats.get(id);
    if (!existing) {
      this.paintStats.set(id, { last: durationMs, max: durationMs, ewma: durationMs });
      return;
    }
    existing.last = durationMs;
    if (durationMs > existing.max) existing.max = durationMs;
    existing.ewma += EWMA_ALPHA * (durationMs - existing.ewma);
  }

  /** Called once per Scheduler frame: records the gap since the previous frame and flags drops. */
  frameTick(): void {
    if (!this.enabled) return;
    const now = this.now();
    if (this.lastFrameTime !== null) {
      const interval = now - this.lastFrameTime;
      this.lastFrameInterval = interval;
      if (interval > DROPPED_FRAME_THRESHOLD_MS) this.droppedFrames++;
    }
    this.lastFrameTime = now;
  }

  /** Count one inbound WS snapshot/delta message (topic currently unused — aggregate rate only). */
  countMessage(topic: string): void {
    if (!this.enabled) return;
    void topic; // kept in the signature for call-site clarity; no per-topic breakdown (keep this diagnostic tool simple)
    this.rollMsgWindow(this.now());
    this.msgWindowCount++;
  }

  /** Count n ticks delivered in a single md.tape message payload. */
  countTicks(n: number): void {
    if (!this.enabled) return;
    this.rollTickWindow(this.now());
    this.tickWindowCount += n;
  }

  /** Record buildTapeRows's scanned-loop-iterations count for one surface (temporary stat). */
  recordScan(id: string, scanned: number): void {
    if (!this.enabled) return;
    const existing = this.scanStats.get(id);
    if (!existing) {
      this.scanStats.set(id, { last: scanned, max: scanned });
      return;
    }
    existing.last = scanned;
    if (scanned > existing.max) existing.max = scanned;
  }

  /** Plain-object view for the HUD to render. Cheap to call on a ~250ms poll; not for the hot path. */
  snapshot(): PerfSnapshot {
    if (!this.enabled) return EMPTY_SNAPSHOT;
    const now = this.now();
    this.rollMsgWindow(now);
    this.rollTickWindow(now);
    return {
      enabled: true,
      paint: mapToObject(this.paintStats),
      scan: mapToObject(this.scanStats),
      frame: { intervalMs: this.lastFrameInterval, droppedFrames: this.droppedFrames },
      wsMsgsPerSec: this.msgsPerSec,
      ticksPerSec: this.ticksPerSec,
    };
  }

  private rollMsgWindow(now: number): void {
    if (now - this.msgWindowStart >= ROLLING_WINDOW_MS) {
      this.msgsPerSec = this.msgWindowCount;
      this.msgWindowCount = 0;
      this.msgWindowStart = now;
    }
  }

  private rollTickWindow(now: number): void {
    if (now - this.tickWindowStart >= ROLLING_WINDOW_MS) {
      this.ticksPerSec = this.tickWindowCount;
      this.tickWindowCount = 0;
      this.tickWindowStart = now;
    }
  }
}

function mapToObject<T>(m: Map<string, T>): Record<string, T> {
  const out: Record<string, T> = {};
  for (const [k, v] of m) out[k] = v;
  return out;
}

// Shared singleton — every hot-path caller (Scheduler, WsClient, registry,
// TapePanel) imports and uses this instance so one toggle controls all of
// them. Disabled by default: zero overhead until a session explicitly opts
// in via initPerfFromQuery() or the HUD's runtime toggle.
export const perf = new PerfMonitor();

/** Reads `?perf=1` from a query string (e.g. location.search) and enables the shared singleton. */
export function initPerfFromQuery(search: string): void {
  if (new URLSearchParams(search).get("perf") === "1") perf.enable();
}
