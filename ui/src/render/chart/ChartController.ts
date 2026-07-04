import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import type { Palette } from "../palette";
import type { Bar } from "../../wire/contract";
import { chartOptions, candleOptions, volumeOptions } from "./chartTheme";
import { sessionBands } from "./sessions";
import { describeIndicator, withDefaultParams, type IndicatorInstance } from "./indicatorSeries";
import type { FillMarker } from "./diamondMarker";

export interface BarReader { series(symbol: string, timeframe: string): Bar[] }
export interface IndicatorReader { series(instanceId: string): { timeMs: number; value: number }[] }
export interface CommandSender { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }

export interface ChartConfig { symbol: string; timeframe: string }
interface Deps { bars: BarReader; indicators: IndicatorReader; commands: CommandSender }

// LWC wants seconds (UTCTimestamp); our bucketStart is an ISO string.
const toLwcTime = (bucketStart: string): number => Math.floor(Date.parse(bucketStart) / 1000);
const toLwcTimeMs = (ms: number): number => Math.floor(ms / 1000);

export class ChartController {
  private candle!: LwcSeries;
  private volume!: LwcSeries;
  private lastAppliedCount = 0;             // bars applied via setData/update
  private lastAppliedKey = "";              // last bar's bucketStart|close, to detect in-progress change
  private backfilled = false;
  private readonly indicators = new Map<string, { inst: IndicatorInstance; series: Map<string, LwcSeries> }>();

  constructor(
    private facade: ChartApiFacade,
    private palette: Palette,
    private config: ChartConfig,
    private readonly deps: Deps,
  ) {}

  mount(): void {
    this.facade.applyOptions(chartOptions(this.palette));
    this.candle = this.facade.addSeries("candle", candleOptions(this.palette), 0);
    this.volume = this.facade.addSeries("histogram", volumeOptions(this.palette), 0);
  }

  sync(): void {
    const bars = this.deps.bars.series(this.config.symbol, this.config.timeframe);
    this.applyBars(bars);
    this.applyIndicators();
    this.applySessions(bars);
  }

  private applyBars(bars: Bar[]): void {
    if (bars.length === 0) return; // cold symbol — panel shows the hint, not an error
    if (!this.backfilled) {
      this.candle.setData(bars.map(toCandle));
      this.volume.setData(bars.map((b) => toVolume(b, this.palette)));
      this.backfilled = true;
      this.lastAppliedCount = bars.length;
      this.lastAppliedKey = keyOf(bars[bars.length - 1]);
      return;
    }
    const last = bars[bars.length - 1];
    const grew = bars.length > this.lastAppliedCount;
    const lastChanged = keyOf(last) !== this.lastAppliedKey;
    if (grew) {
      // Push every newly-appended bar in order — update() only appends/replaces the
      // single bar it's given, so a multi-bucket jump (backgrounded tab, missed rAF
      // tick, reconnect burst) must be replayed bar-by-bar or the gap is permanent.
      for (let i = this.lastAppliedCount; i < bars.length; i++) {
        this.candle.update(toCandle(bars[i]));
        this.volume.update(toVolume(bars[i], this.palette));
      }
      this.lastAppliedCount = bars.length;
      this.lastAppliedKey = keyOf(last);
    } else if (lastChanged) {
      this.candle.update(toCandle(last));
      this.volume.update(toVolume(last, this.palette));
      this.lastAppliedKey = keyOf(last);
      // Auto-follow is LWC's default when already at the right edge; never force it
      // when the user has scrolled back (honesty: don't yank their view).
    }
  }

  private applyIndicators(): void {
    for (const { inst, series } of this.indicators.values()) {
      const descriptors = describeIndicator(inst, this.palette);
      for (const d of descriptors) {
        const s = series.get(d.key);
        if (!s) continue;
        // For MACD's multi-series the engine streams each sub-series under its own
        // instanceId suffix; single-series indicators use the base instanceId.
        const points = this.deps.indicators.series(d.key);
        s.setData(points.map((p) => ({ time: toLwcTimeMs(p.timeMs), value: p.value })));
      }
    }
  }

  private applySessions(bars: Bar[]): void {
    if (bars.length === 0) { this.facade.setSessionBands([]); return; }
    const from = Date.parse(bars[0].bucketStart);
    const to = Date.parse(bars[bars.length - 1].bucketStart) + 1;
    this.facade.setSessionBands(sessionBands(from, to));
  }

  addIndicator(inst: IndicatorInstance): void {
    // Resolve any unset params to catalog defaults so the engine always gets a
    // complete param set (and the stored instance matches what's rendered).
    const resolved: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    const series = new Map<string, LwcSeries>();
    for (const d of describeIndicator(resolved, this.palette)) {
      series.set(d.key, this.facade.addSeries(d.kind === "histogram" ? "histogram" : "line",
        { color: d.color, priceScaleId: d.paneIndex === 0 && d.kind === "histogram" ? "" : undefined }, d.paneIndex));
    }
    this.indicators.set(resolved.instanceId, { inst: resolved, series });
    void this.deps.commands.sendCommand("SubscribeIndicator", {
      instanceId: resolved.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
      type: resolved.type, params: resolved.params,
    });
  }

  removeIndicator(instanceId: string): void {
    const entry = this.indicators.get(instanceId);
    if (!entry) return;
    for (const s of entry.series.values()) this.facade.removeSeries(s);
    this.indicators.delete(instanceId);
    void this.deps.commands.sendCommand("UnsubscribeIndicator", { instanceId });
  }

  // Apply an edited instance. A param change re-subscribes (the engine recomputes
  // the series); a color-only change just re-applies each slot's color in place —
  // no re-subscribe, so the line doesn't blink.
  updateIndicator(inst: IndicatorInstance): void {
    const existing = this.indicators.get(inst.instanceId);
    if (!existing) { this.addIndicator(inst); return; }
    const next: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    if (JSON.stringify(existing.inst.params) !== JSON.stringify(next.params)) {
      this.removeIndicator(inst.instanceId);
      this.addIndicator(next);
      return;
    }
    existing.inst = next; // colors only
    for (const d of describeIndicator(next, this.palette)) existing.series.get(d.key)?.applyOptions({ color: d.color });
  }

  setSymbol(symbol: string): void { this.config = { ...this.config, symbol }; this.resetForReload(); }
  setTimeframe(timeframe: string): void { this.config = { ...this.config, timeframe }; this.resetForReload(); }

  private resetForReload(): void {
    this.backfilled = false;
    this.lastAppliedCount = 0;
    this.lastAppliedKey = "";
    // Re-subscribe every live indicator for the new (symbol, timeframe).
    for (const { inst } of this.indicators.values()) {
      void this.deps.commands.sendCommand("SubscribeIndicator", {
        instanceId: inst.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
        type: inst.type, params: inst.params,
      });
    }
  }

  setPalette(p: Palette): void {
    this.palette = p;
    this.facade.applyOptions(chartOptions(p));
    this.candle.applyOptions(candleOptions(p));
    this.volume.applyOptions(volumeOptions(p));
    for (const { inst, series } of this.indicators.values())
      for (const d of describeIndicator(inst, p)) series.get(d.key)?.applyOptions({ color: d.color });
  }

  setFills(markers: FillMarker[]): void { this.facade.setFillMarkers(markers); }
  resize(w: number, h: number): void { this.facade.resize(w, h); }
  jumpToLive(): void { this.facade.scrollToRealTime(); }
  dispose(): void {
    for (const id of [...this.indicators.keys()]) this.removeIndicator(id);
    this.facade.remove();
  }
}

function keyOf(b: Bar): string { return `${b.bucketStart}|${b.c}|${b.h}|${b.l}|${b.v}|${b.inProgress}`; }
function toCandle(b: Bar) { return { time: toLwcTime(b.bucketStart), open: b.o, high: b.h, low: b.l, close: b.c }; }
function toVolume(b: Bar, p: Palette) {
  return { time: toLwcTime(b.bucketStart), value: b.v, color: b.c >= b.o ? p.volUp : p.volDown };
}
