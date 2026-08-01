// ui/src/render/chart/olderHistory.ts

/** Which side of the history split a request targets. Independent guard state per kind. */
export type HistoryKind = "intraday" | "daily";

/** Ack shape returned by the engine's LoadOlderBars command. */
export interface OlderHistoryAck {
  status: string;
  reason?: string;
  value?: unknown;
}

export interface OlderHistoryDeps {
  /** Wraps commands.sendCommand("LoadOlderBars", { daily, requiredStartMs, requiredEndMs }) — resolves with the ack. */
  load: (daily: boolean, requiredStartMs: number, requiredEndMs: number) => Promise<OlderHistoryAck>;
  /** Injected clock, so cooldown/timeout logic is deterministic in tests. */
  now: () => number;
}

const COOLDOWN_MS = 5_000;
const TIMEOUT_MS = 30_000;

/**
 * Fires only when called explicitly after gesture release (pointerup, keyup,
 * wheel idle), not on every range-change event. Guards against duplicate/
 * looping LoadOlderBars requests. UI-framework-agnostic and fully
 * unit-testable via injected `load`/`now`; ChartPanel wires it to
 * commands.sendCommand("LoadOlderBars", ...) plus the chart's visible range
 * and current timeframe/symbol.
 *
 * Guards:
 *  - one request in flight at a time PER KIND (intraday vs daily are independent)
 *  - once a kind is `exhausted` (an accepted ack with value.exhausted: true), it
 *    is never asked again until `reset()` (symbol change)
 *  - a ~5s cooldown after a `blocked` ack before retrying that kind
 *  - a 30s timeout clears the in-flight flag if no ack ever arrives (e.g. a lost
 *    ack across a reconnect) — this never clears the exhausted flag
 */
export class OlderHistoryController {
  private readonly inflight: Record<HistoryKind, boolean> = { intraday: false, daily: false };
  private readonly exhausted: Record<HistoryKind, boolean> = { intraday: false, daily: false };
  private readonly cooldownUntil: Record<HistoryKind, number> = { intraday: 0, daily: 0 };
  private readonly lastAcceptedStartMs: Record<HistoryKind, number | null> = { intraday: null, daily: null };
  private readonly timers: Record<HistoryKind, ReturnType<typeof setTimeout> | undefined> = {
    intraday: undefined,
    daily: undefined,
  };

  constructor(private readonly deps: OlderHistoryDeps) {}

  /**
   * Called once after the user releases a pan/zoom gesture (pointerup, keyup,
   * wheel idle). Fires LoadOlderBars if guards pass. Safe to call speculatively
   * — inflight/exhausted/cooldown/dedup guards prevent duplicate requests.
   * Returns a promise that resolves when the request settles (ack received or
   * timeout), so callers can read exhausted state afterwards.
   */
  triggerNow(
    isIntraday: boolean,
    requiredRangeMs: { from: number; to: number } | null = null,
  ): Promise<void> {
    const kind: HistoryKind = isIntraday ? "intraday" : "daily";
    if (this.inflight[kind] || this.exhausted[kind]) return Promise.resolve();
    if (this.deps.now() < this.cooldownUntil[kind]) return Promise.resolve();
    if (!requiredRangeMs) return Promise.resolve();

    const requiredStartMs = requiredRangeMs.from;
    if (this.lastAcceptedStartMs[kind] === requiredStartMs) return Promise.resolve();

    this.inflight[kind] = true;
    this.clearTimer(kind);
    this.timers[kind] = setTimeout(() => {
      // Lost ack (e.g. across a reconnect): stop blocking new requests, but
      // leave `exhausted` untouched — that flag is only ever set by an
      // explicit accepted+exhausted ack.
      this.inflight[kind] = false;
      this.timers[kind] = undefined;
    }, TIMEOUT_MS);

    const daily = kind === "daily";
    return this.deps
      .load(daily, requiredStartMs, requiredRangeMs.to)
      .then((ack) => this.settle(kind, ack, requiredStartMs))
      .catch((err) => { this.settle(kind, { status: "blocked" }, requiredStartMs); throw err; });
  }

  isExhausted(kind: HistoryKind): boolean {
    return this.exhausted[kind];
  }

  /** Clears all guard state for both kinds. Call on symbol change. */
  reset(): void {
    this.inflight.intraday = false;
    this.inflight.daily = false;
    this.exhausted.intraday = false;
    this.exhausted.daily = false;
    this.lastAcceptedStartMs.intraday = null;
    this.lastAcceptedStartMs.daily = null;
    this.cooldownUntil.intraday = 0;
    this.cooldownUntil.daily = 0;
    this.clearTimer("intraday");
    this.clearTimer("daily");
  }

  private clearTimer(kind: HistoryKind): void {
    const t = this.timers[kind];
    if (t !== undefined) {
      clearTimeout(t);
      this.timers[kind] = undefined;
    }
  }

  private settle(kind: HistoryKind, ack: OlderHistoryAck, requiredStartMs: number): void {
    this.clearTimer(kind);
    this.inflight[kind] = false;
    if (ack.status === "accepted") {
      this.lastAcceptedStartMs[kind] = requiredStartMs;
      const value = ack.value as { exhausted?: boolean } | undefined;
      if (value?.exhausted) this.exhausted[kind] = true;
    } else {
      this.cooldownUntil[kind] = this.deps.now() + COOLDOWN_MS;
    }
  }
}
