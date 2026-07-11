// ui/src/render/chart/olderHistory.ts
import { LEFT_PAD_BARS } from "./ChartController";

/** Which side of the history split a request targets. Independent guard state per kind. */
export type HistoryKind = "intraday" | "daily";

/** Ack shape returned by the engine's LoadOlderBars command. */
export interface OlderHistoryAck {
  status: string;
  reason?: string;
  value?: unknown;
}

export interface OlderHistoryDeps {
  /** Wraps commands.sendCommand("LoadOlderBars", { daily }) — resolves with the ack. */
  load: (daily: boolean) => Promise<OlderHistoryAck>;
  /** Injected clock, so cooldown/timeout logic is deterministic in tests. */
  now: () => number;
}

const SCREENS_THRESHOLD = 1.5;
const COOLDOWN_MS = 5_000;
const TIMEOUT_MS = 30_000;

/**
 * Decides when to ask the engine for an older chunk of history bars as the
 * user pans a chart left, and guards against duplicate/looping LoadOlderBars
 * requests. UI-framework-agnostic and fully unit-testable via injected
 * `load`/`now`; ChartPanel (Task 12) wires it to
 * commands.sendCommand("LoadOlderBars", ...) plus the chart's visible range
 * and current timeframe/symbol.
 *
 * Guards:
 *  - fires only when fewer than ~1.5 screens of bars remain left of the viewport
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
  private readonly timers: Record<HistoryKind, ReturnType<typeof setTimeout> | undefined> = {
    intraday: undefined,
    daily: undefined,
  };

  constructor(private readonly deps: OlderHistoryDeps) {}

  maybeTrigger(range: { from: number; to: number } | null, isIntraday: boolean): void {
    if (!range) return;
    const kind: HistoryKind = isIntraday ? "intraday" : "daily";
    if (this.inflight[kind] || this.exhausted[kind]) return;
    if (this.deps.now() < this.cooldownUntil[kind]) return;

    const screens = range.to - range.from;
    if (screens <= 0) return;
    const remaining = range.from - LEFT_PAD_BARS;
    if (remaining >= SCREENS_THRESHOLD * screens) return;

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
    this.deps
      .load(daily)
      .then((ack) => this.settle(kind, ack))
      .catch(() => this.settle(kind, { status: "blocked" }));
  }

  /** Clears all guard state for both kinds. Call on symbol change. */
  reset(): void {
    this.inflight.intraday = false;
    this.inflight.daily = false;
    this.exhausted.intraday = false;
    this.exhausted.daily = false;
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

  private settle(kind: HistoryKind, ack: OlderHistoryAck): void {
    this.clearTimer(kind);
    this.inflight[kind] = false;
    if (ack.status === "accepted") {
      const value = ack.value as { exhausted?: boolean } | undefined;
      if (value?.exhausted) this.exhausted[kind] = true;
    } else {
      this.cooldownUntil[kind] = this.deps.now() + COOLDOWN_MS;
    }
  }
}
