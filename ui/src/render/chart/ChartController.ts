import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import type { Palette } from "../palette";
import type { Bar } from "../../wire/contract";
import { chartOptions, candleOptions, volumeOptions, mainSeriesOptions, VOLUME_SCALE_MARGINS, OVERLAY_NO_AUTOSCALE, type ChartType } from "./chartTheme";
import { sessionAt } from "./sessions";
import type { Band } from "./sessions";
import { describeIndicator, withDefaultParams, type IndicatorInstance } from "./indicatorSeries";
import { LWC_LINE_STYLE } from "./lineStyle";
import type { FillMarker } from "./diamondMarker";
import { timeframeToMs } from "./drawings/geometry";
import type { Timeframe } from "./barBucket";

export interface BarReader { series(symbol: string, timeframe: string): Bar[] }
export interface IndicatorReader { series(instanceId: string): { timeMs: number; value: number }[] }
export interface CommandSender { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }

export interface ChartConfig { symbol: string; timeframe: string }
interface Deps { bars: BarReader; indicators: IndicatorReader; commands: CommandSender }

// LWC wants seconds (UTCTimestamp); our bucketStart is an ISO string.
const toLwcTime = (bucketStart: string): number => Math.floor(Date.parse(bucketStart) / 1000);
const toLwcTimeMs = (ms: number): number => Math.floor(ms / 1000);

// Empty bars of whitespace kept past both edges of the loaded data: to the right
// via LWC's native `rightOffset` (chartTheme), and to the left by prepending this
// many WhitespaceData points ahead of the earliest real bar (LWC has no left-offset
// option) paired with `fixLeftEdge` so the farthest-left pan stops there. Exported
// so tests can assert against it instead of a repeated magic number.
export const LEFT_PAD_BARS = 4;

export class ChartController {
  private candle!: LwcSeries;
  private volume!: LwcSeries;
  private lastAppliedCount = 0;             // bars applied via setData/update
  private lastAppliedKey = "";              // last bar's bucketStart|close, to detect in-progress change
  private indicatorApplied = new Map<string, number>(); // per-series point count applied via setData/update
  private indicatorLastKey = new Map<string, string>(); // per-series fingerprint of the last applied point, `${timeMs}|${value}`
  private backfilled = false;
  private chartType: ChartType = "candle";
  private readonly indicators = new Map<string, { inst: IndicatorInstance; series: Map<string, LwcSeries> }>();

  constructor(
    private facade: ChartApiFacade,
    private palette: Palette,
    private config: ChartConfig,
    private readonly deps: Deps,
  ) {}

  mount(): void {
    this.facade.applyOptions(chartOptions(this.palette));
    this.candle = this.facade.setMainSeries("candle", candleOptions(this.palette));
    this.volume = this.facade.addSeries("histogram", volumeOptions(this.palette), 0);
    // Confine the volume overlay to the bottom band of the main pane so it never
    // overlaps the candles (the candle scale reserves the same band — see chartTheme).
    this.facade.setPriceScaleMargins("", VOLUME_SCALE_MARGINS);
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
      this.setAllBars(bars);
      return;
    }
    const last = bars[bars.length - 1];
    const grew = bars.length > this.lastAppliedCount;
    const lastChanged = keyOf(last) !== this.lastAppliedKey;
    if (grew) {
      // Push every newly-appended bar in order — update() only appends/replaces the
      // single bar it's given, so a multi-bucket jump (backgrounded tab, missed rAF
      // tick, reconnect burst) must be replayed bar-by-bar or the gap is permanent.
      // Start one bar before `lastAppliedCount`: that bar was `last` as of the previous
      // applied state and may have itself changed (e.g. finalized) during the same
      // missed window that produced the new bars — re-flushing it is harmless/idempotent
      // if unchanged, and necessary if it did change.
      const from = Math.max(0, this.lastAppliedCount - 1);
      if (!isSorted(bars, from)) {
        // BarStore keeps its series sorted, so this should never fire — but LWC's
        // series.update() throws on a non-monotonic time, and that throw used to
        // permanently freeze this chart (Scheduler used to drop a panel on its first
        // paint error). Rebuilding via setData is always safe, so prefer a full
        // resync over ever risking that throw.
        this.setAllBars(bars);
        return;
      }
      for (let i = from; i < bars.length; i++) {
        this.candle.update(this.mainPoint(bars[i]));
        this.volume.update(toVolume(bars[i], this.palette));
      }
      this.lastAppliedCount = bars.length;
      this.lastAppliedKey = keyOf(last);
    } else if (lastChanged) {
      this.candle.update(this.mainPoint(last));
      this.volume.update(toVolume(last, this.palette));
      this.lastAppliedKey = keyOf(last);
      // Auto-follow is LWC's default when already at the right edge; never force it
      // when the user has scrolled back (honesty: don't yank their view).
    }
  }

  private setAllBars(bars: Bar[]): void {
    const pad = this.leftPad(bars);
    this.candle.setData([...pad, ...bars.map((b) => this.mainPoint(b))]);
    this.volume.setData([...pad, ...bars.map((b) => toVolume(b, this.palette))]);
    this.backfilled = true;
    // lastAppliedCount/lastAppliedKey track the REAL bars only — the incremental
    // applyBars path above indexes into `bars` (the BarReader's series), which
    // never includes this padding.
    this.lastAppliedCount = bars.length;
    this.lastAppliedKey = keyOf(bars[bars.length - 1]);
  }

  // LEFT_PAD_BARS WhitespaceData points (time-only, no OHLC — valid for both the
  // candle and volume series in LWC v5) placed before the earliest real bar, so
  // with fixLeftEdge the farthest-left pan leaves the same empty margin the right
  // edge already gets from rightOffset. Span is derived from the loaded bars
  // (self-adjusts to the active timeframe); falls back to the nominal timeframe
  // span when fewer than 2 bars are loaded (can't derive a step from one point).
  private leftPad(bars: Bar[]): { time: number }[] {
    const t0 = toLwcTime(bars[0].bucketStart);
    const spanMs = bars.length > 1
      ? Date.parse(bars[1].bucketStart) - Date.parse(bars[0].bucketStart)
      : timeframeToMs(this.config.timeframe as Timeframe);
    const spanSec = Math.max(1, Math.floor(spanMs / 1000));
    return Array.from({ length: LEFT_PAD_BARS }, (_, i) => ({ time: t0 - (LEFT_PAD_BARS - i) * spanSec }));
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
        const applied = this.indicatorApplied.get(d.key) ?? 0;
        const last = points[points.length - 1];
        const lastKey = last ? `${last.timeMs}|${last.value}` : "";
        if (applied === 0 || points.length < applied) {
          // First application, or the series shrank (e.g. a full recompute produced
          // fewer points) — only setData() is safe.
          s.setData(points.map((p) => ({ time: toLwcTimeMs(p.timeMs), value: p.value })));
        } else if (points.length > applied) {
          // Re-flush from one index before `applied`: that point was `last` as of the
          // previous applied state and may have been revised (in-progress-bar upsert)
          // during the same missed rAF-coalesced window that also appended new points —
          // re-flushing it is harmless/idempotent if unchanged, and necessary if it
          // did change (mirrors applyBars's identical race-window guard).
          for (let i = Math.max(0, applied - 1); i < points.length; i++) {
            s.update({ time: toLwcTimeMs(points[i].timeMs), value: points[i].value });
          }
        } else if (last && lastKey !== this.indicatorLastKey.get(d.key)) {
          // Same length, but the last point's value changed in place — the
          // in-progress-bar revision case (IndicatorStore upserts, doesn't append).
          s.update({ time: toLwcTimeMs(last.timeMs), value: last.value });
        }
        this.indicatorApplied.set(d.key, points.length);
        this.indicatorLastKey.set(d.key, lastKey);
      }
    }
  }

  // Bands are built from the loaded bars' own bucketStart times, not fixed
  // wall-clock session boundaries (sessions.ts's sessionBands): the session
  // primitive resolves each band edge via LWC's timeToCoordinate, which returns
  // null unless the edge is an EXACT bar time. Wall-clock boundaries (04:00,
  // 09:30, 16:00, 20:00 ET) only land on a real bar when the timeframe's bucket
  // grid happens to include them — true for 10s/1m (midnight-anchored) and
  // 5m/15m/30m (09:30-anchored, still an exact multiple of 04:00), but NEVER
  // true for 60m (09:30-anchored: pre-market buckets fall at 03:30/04:30/…, so
  // 04:00 is never a bucket start) — that mismatch silently dropped the whole
  // band, leaving 60m unshaded. Deriving edges from the bars themselves makes
  // every edge a real bar time on every timeframe.
  private applySessions(bars: Bar[]): void {
    const intraday = !["D", "W", "M"].includes(this.config.timeframe);
    if (!intraday || bars.length === 0) { this.facade.setSessionBands([]); return; }
    this.facade.setSessionBands(bandsFromBars(bars));
  }

  addIndicator(inst: IndicatorInstance): void {
    // Resolve any unset params to catalog defaults so the engine always gets a
    // complete param set (and the stored instance matches what's rendered).
    const resolved: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    const series = new Map<string, LwcSeries>();
    for (const d of describeIndicator(resolved, this.palette)) {
      series.set(d.key, this.facade.addSeries(d.kind === "histogram" ? "histogram" : "line",
        {
          color: d.color,
          priceScaleId: d.paneIndex === 0 && d.kind === "histogram" ? "" : undefined,
          // Studies read as reference lines, not standalone series — no chart-spanning
          // last-value price line (TradingView doesn't draw one for overlay indicators).
          priceLineVisible: false,
          visible: !(resolved.hidden ?? false),
          ...(d.kind === "line" ? { lineWidth: d.width, lineStyle: LWC_LINE_STYLE[d.lineStyle] } : {}),
          // Main-pane overlay lines (EMA/SMA/VWAP) share the candle price scale but
          // must never expand its autoscale range — see OVERLAY_NO_AUTOSCALE. MACD's
          // sub-pane lines (paneIndex 1) are excluded: they must autoscale their own pane.
          ...(d.kind === "line" && d.paneIndex === 0 ? { autoscaleInfoProvider: OVERLAY_NO_AUTOSCALE } : {}),
        }, d.paneIndex));
    }
    this.indicators.set(resolved.instanceId, { inst: resolved, series });
    this.subscribeIndicator(resolved);
    this.liftCandleToTop();
  }

  // Keep the candle painted over main-pane overlay indicators (VWAP/EMA/SMA).
  // LWC draws series within a pane by ascending seriesOrder index — the candle
  // is created first (order 0) and every later indicator would otherwise sit on
  // top of it. Setting an out-of-range index clamps to the current top slot, and
  // since removing a series can recalc indices, both addIndicator and
  // removeIndicator re-assert this.
  private liftCandleToTop(): void {
    this.candle.setSeriesOrder(Number.MAX_SAFE_INTEGER);
  }

  private subscribeIndicator(inst: IndicatorInstance): void {
    void this.deps.commands.sendCommand("SubscribeIndicator", {
      instanceId: inst.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
      type: inst.type, params: inst.params,
    });
  }

  removeIndicator(instanceId: string): void {
    const entry = this.indicators.get(instanceId);
    if (!entry) return;
    for (const s of entry.series.values()) this.facade.removeSeries(s);
    for (const k of entry.series.keys()) {
      this.indicatorApplied.delete(k);
      this.indicatorLastKey.delete(k);
    }
    this.indicators.delete(instanceId);
    void this.deps.commands.sendCommand("UnsubscribeIndicator", { instanceId });
    this.liftCandleToTop();
  }

  // Apply an edited instance. A param change re-subscribes (the engine recomputes
  // the series); a style-only change (color/width/lineStyle/hidden) just re-applies
  // each slot's options in place — no re-subscribe, so the line doesn't blink.
  updateIndicator(inst: IndicatorInstance): void {
    const existing = this.indicators.get(inst.instanceId);
    if (!existing) { this.addIndicator(inst); return; }
    const next: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    if (JSON.stringify(existing.inst.params) !== JSON.stringify(next.params)) {
      this.removeIndicator(inst.instanceId);
      this.addIndicator(next);
      return;
    }
    existing.inst = next; // params unchanged → style/visibility only, applied in place (no re-subscribe)
    const hidden = next.hidden ?? false;
    for (const d of describeIndicator(next, this.palette)) {
      existing.series.get(d.key)?.applyOptions({
        color: d.color,
        visible: !hidden,
        ...(d.kind === "line" ? { lineWidth: d.width, lineStyle: LWC_LINE_STYLE[d.lineStyle] } : {}),
      });
    }
  }

  setSymbol(symbol: string): void { this.config = { ...this.config, symbol }; this.resetForReload(); }
  setTimeframe(timeframe: string): void { this.config = { ...this.config, timeframe }; this.resetForReload(); }

  private resetForReload(): void {
    this.backfilled = false;
    this.lastAppliedCount = 0;
    this.lastAppliedKey = "";
    this.indicatorApplied.clear();
    this.indicatorLastKey.clear();
    // Wipe the previous (symbol, timeframe)'s bars immediately — otherwise a
    // switch to a series that's empty or slow to arrive (e.g. Daily -> a cold
    // 1m symbol) leaves the old timeframe's candles frozen on screen forever
    // (applyBars early-returns on an empty series, so it would never clear them).
    this.candle.setData([]);
    this.volume.setData([]);
    this.facade.setSessionBands([]);
    // Re-subscribe every live indicator for the new (symbol, timeframe).
    for (const { inst } of this.indicators.values()) this.subscribeIndicator(inst);
  }

  setPalette(p: Palette): void {
    this.palette = p;
    this.facade.applyOptions(chartOptions(p));
    this.candle.applyOptions(mainSeriesOptions(this.chartType, p));
    this.volume.applyOptions(volumeOptions(p));
    for (const { inst, series } of this.indicators.values())
      for (const d of describeIndicator(inst, p)) series.get(d.key)?.applyOptions({ color: d.color });
  }

  // Main-series data point matched to the active chart type: OHLC for candle/bar,
  // single close value for line/area (LWC line/area series read `.value`).
  private mainPoint(b: Bar): object {
    return this.chartType === "line" || this.chartType === "area"
      ? { time: toLwcTime(b.bucketStart), value: b.c }
      : toCandle(b);
  }

  setChartType(type: ChartType): void {
    if (type === this.chartType) return;
    this.chartType = type;
    this.candle = this.facade.setMainSeries(type, mainSeriesOptions(type, this.palette));
    // Force a full re-seed of the new series on the next sync().
    this.backfilled = false;
    this.lastAppliedCount = 0;
    this.lastAppliedKey = "";
    this.liftCandleToTop();
  }

  setFills(markers: FillMarker[]): void { this.facade.setFillMarkers(markers); }
  resize(w: number, h: number): void { this.facade.resize(w, h); }
  jumpToLive(): void { this.facade.scrollToRealTime(); }
  resetZoom(): void { this.facade.resetTimeScale(); }
  dispose(): void {
    for (const id of [...this.indicators.keys()]) this.removeIndicator(id);
    this.facade.remove();
  }
}

function keyOf(b: Bar): string { return `${b.bucketStart}|${b.c}|${b.h}|${b.l}|${b.v}|${b.inProgress}`; }
// Whether bars[from..] is non-decreasing by bucketStart — the property update()'s
// bar-by-bar replay depends on to never hand Lightweight Charts a time that goes
// backwards relative to what it was already given.
function isSorted(bars: Bar[], from: number): boolean {
  for (let i = Math.max(from, 1); i < bars.length; i++) {
    if (bars[i].bucketStart < bars[i - 1].bucketStart) return false;
  }
  return true;
}
function toCandle(b: Bar) { return { time: toLwcTime(b.bucketStart), open: b.o, high: b.h, low: b.l, close: b.c }; }
function toVolume(b: Bar, p: Palette) {
  return { time: toLwcTime(b.bucketStart), value: b.v, color: b.c >= b.o ? p.volUp : p.volDown };
}

// One band per contiguous run of same-session bars, with every edge pinned to a
// real bar's bucketStart (see the applySessions comment above for why: the
// session primitive drops a band whose edge doesn't land on an exact bar time).
// The final band's end is the LAST bar's own time, not lastBar+span — extending
// past the last bar would reintroduce the same null-coordinate problem this
// function exists to avoid.
function bandsFromBars(bars: Bar[]): Band[] {
  const bands: Band[] = [];
  for (const b of bars) {
    const startMs = Date.parse(b.bucketStart);
    const session = sessionAt(startMs);
    const cur = bands[bands.length - 1];
    if (cur && cur.session === session) continue; // still inside the same run
    if (cur) cur.endMs = startMs; // close the previous run at this bar
    bands.push({ startMs, endMs: startMs, session });
  }
  const last = bands[bands.length - 1];
  if (last) last.endMs = Date.parse(bars[bars.length - 1].bucketStart);
  return bands;
}
