export interface MarketClockUpdate {
  effectiveOffsetMs: number;
  browserToEngineOffsetMs: number;
  marketOffsetMs: number;
  engineTimeMs: number;
  browserRttMs: number;
  marketSampleAgeMs: number;
  marketSampleRttMs: number;
}

export interface MarketClockSnapshot {
  synchronized: boolean;
  stale: boolean;
  offsetMs: number;
  browserToEngineOffsetMs: number | null;
  marketOffsetMs: number | null;
  engineTimeMs: number | null;
  browserRttMs: number | null;
  marketSampleAgeMs: number | null;
  marketSampleRttMs: number | null;
}

export const MARKET_CLOCK_STALE_MS = 15_000;
const MAX_CLOCK_OFFSET_MS = 24 * 60 * 60 * 1000;

// Imperative chart-time projection. It deliberately has no React/store
// notifications: the scheduler and the 1Hz countdown read nowMs() when they
// already have work to do, while pong updates only replace this small snapshot.
export class MarketClock {
  private offset = 0;
  private synchronized = false;
  private receivedAtMs = 0;
  private sampleAgeAtReceiptMs = 0;
  private browserToEngineOffset: number | null = null;
  private marketOffset: number | null = null;
  private engineTime: number | null = null;
  private browserRtt: number | null = null;
  private marketSampleRtt: number | null = null;

  constructor(private readonly now: () => number = Date.now) {}

  nowMs = (): number => this.now() + this.offset;

  update(sample: MarketClockUpdate): void {
    if (!Number.isFinite(sample.effectiveOffsetMs)
      || Math.abs(sample.effectiveOffsetMs) > MAX_CLOCK_OFFSET_MS
      || !Number.isFinite(sample.marketSampleAgeMs)
      || sample.marketSampleAgeMs < 0) return;

    const receivedAt = this.now();
    this.offset = sample.effectiveOffsetMs;
    this.synchronized = true;
    this.receivedAtMs = receivedAt;
    this.sampleAgeAtReceiptMs = sample.marketSampleAgeMs;
    this.browserToEngineOffset = sample.browserToEngineOffsetMs;
    this.marketOffset = sample.marketOffsetMs;
    this.engineTime = sample.engineTimeMs;
    this.browserRtt = sample.browserRttMs;
    this.marketSampleRtt = sample.marketSampleRttMs;
  }

  snapshot(): MarketClockSnapshot {
    const age = this.synchronized
      ? this.sampleAgeAtReceiptMs + Math.max(0, this.now() - this.receivedAtMs)
      : null;
    return {
      synchronized: this.synchronized,
      stale: age !== null && age > MARKET_CLOCK_STALE_MS,
      offsetMs: this.offset,
      browserToEngineOffsetMs: this.browserToEngineOffset,
      marketOffsetMs: this.marketOffset,
      engineTimeMs: this.engineTime,
      browserRttMs: this.browserRtt,
      marketSampleAgeMs: age,
      marketSampleRttMs: this.marketSampleRtt,
    };
  }
}
