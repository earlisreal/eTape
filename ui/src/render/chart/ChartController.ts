import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import type { Palette } from "../palette";
import type { Bar } from "../../wire/contract";
import {
  chartOptions, candleOptions, volumeOptions, mainSeriesOptions, VOLUME_SCALE_MARGINS,
  boundedOverlayAutoscale, OVERLAY_AUTOSCALE_FACTOR, type ChartType, type PriceRange,
} from "./chartTheme";
import { sessionAt, buildDaySegment, classify } from "./sessions";
import type { Band, DaySegment } from "./sessions";
import { describeIndicator, withDefaultParams, type IndicatorInstance } from "./indicatorSeries";
import { LWC_LINE_STYLE } from "./lineStyle";
import type { FillMarker } from "./diamondMarker";
import { timeframeToMs } from "./drawings/geometry";
import type { Timeframe } from "./barBucket";

export interface BarReader { series(symbol: string, timeframe: string): Bar[] }
export interface IndicatorReader { series(instanceId: string): { timeMs: number; value: number }[] }
// IndicatorReader plus the ability to drop a series — resetForReload uses this to
// wipe the previous symbol's points instead of leaving them stranded in the shared
// store (see resetForReload). Kept separate from IndicatorReader so read-only
// consumers (e.g. legendView) aren't forced to implement reset().
export interface IndicatorController extends IndicatorReader { reset(instanceId: string): void }
export interface CommandSender { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }

export interface ChartConfig { symbol: string; timeframe: string }
interface Deps { bars: BarReader; indicators: IndicatorController; commands: CommandSender }

// LWC wants seconds (UTCTimestamp); our bucketStart is an ISO string.
const toLwcTime = (bucketStart: string): number => Math.floor(Date.parse(bucketStart) / 1000);
const toLwcTimeMs = (ms: number): number => Math.floor(ms / 1000);

// Empty bars of whitespace kept past both edges of the loaded data: to the right
// via LWC's native `rightOffset` (chartTheme), and to the left by prepending this
// many WhitespaceData points ahead of the earliest real bar (LWC has no left-offset
// option) paired with `fixLeftEdge` so the farthest-left pan stops there. Exported
// so tests can assert against it instead of a repeated magic number.
export const LEFT_PAD_BARS = 4;

// Stretch factor a "collapsed" sub-pane (e.g. MACD) is pinned to — small enough to
// read as a thin strip but non-zero so LWC never treats the pane as empty/removable.
export const COLLAPSED_STRETCH = 0.06;

export class ChartController {
  private candle!: LwcSeries;
  private volume!: LwcSeries;
  private lastAppliedCount = 0;             // bars applied via setData/update
  private lastAppliedKey = "";              // last bar's bucketStart|close, to detect in-progress change
  // bucketStart of the bar at index (lastAppliedCount-1) as of the last apply — an
  // anchor-identity check independent of value, so applyBars can tell a real tail
  // extension from a store snapshot that grew the series at the FRONT (deep-history
  // backfill prepending bars, or a daily series replacing a single derived bar).
  // See applyBars.
  private lastTailBucket = "";
  private indicatorApplied = new Map<string, number>(); // per-series point count applied via setData/update
  private indicatorLastKey = new Map<string, string>(); // per-series fingerprint of the last applied point, `${timeMs}|${value}`
  // per-series timeMs of the point at index (applied-1) as of the last apply — an
  // identity check independent of value, so a same-point value revision (branch 3
  // below) doesn't look like a generation swap. See applyIndicators.
  private indicatorLastAppliedTimeMs = new Map<string, number>();
  private backfilled = false;
  // Live [low, high] across all currently-applied bars — the reference range
  // main-pane overlay lines (EMA/SMA/VWAP) are bounded against (chartTheme's
  // boundedOverlayAutoscale). Recomputed on every applyBars call; cleared on
  // symbol/timeframe switch so a stale range never bounds the new series.
  private candleRange: PriceRange | null = null;
  // --- Per-call memoization (Task 3) -----------------------------------
  // applyBars' outcome for the bars it was just given — set exclusively inside
  // applyBars/setAllBars, read by refreshBarCaches (called right after applyBars
  // in sync()) so the cache-refresh logic can share the same reset/appended/
  // tailUpdated/none classification instead of re-deriving it.
  private lastBarsOp: "reset" | "appended" | "tailUpdated" | "none" = "reset";
  // The index the `grew` branch of applyBars replayed update() from (== the OLD
  // lastAppliedCount-1, captured before that field is advanced) — reused verbatim
  // by refreshBarCaches so the cache fold starts from the exact same bar the LWC
  // replay did, never a second, independently-computed index.
  private appendedFrom = 0;
  // Min/max across bars[0 .. n-2] ONLY — i.e. every bar except the current last
  // one, which may still be live/in-progress. Maintained incrementally (full
  // rescan on "reset", folded forward on "appended", untouched on "tailUpdated"/
  // "none" since the live last bar is excluded either way). candleRange (above)
  // is recombined with the CURRENT last bar's own l/h fresh on every sync() call
  // — see refreshBarCaches — so a same-bar revision (e.g. a spike that later
  // retreats) is always reflected exactly, never stuck at a stale peak.
  private closedRange: PriceRange | null = null;
  // Date.parse(bucketStart) for every bar in the currently-applied series,
  // index-aligned with it. Exposed read-only via barsMs() so other call sites
  // (e.g. the drawings primitive) can reuse it instead of re-parsing.
  private barsMsCache: number[] = [];
  // Same content bandsFromBars(bars) would produce, maintained incrementally.
  private bandsCache: Band[] = [];
  // True whenever bandsCache is NOT guaranteed to reflect the full currently-
  // loaded `bars` — i.e. it needs a from-scratch rebuild (extendBandsFrom(0, …))
  // before it can next be trusted, rather than an incremental extend. Set on
  // every "reset" (a different series may now be loaded) and on every sync where
  // sessions are inactive (see refreshBands — bandsCache maintenance is paused
  // while unused, per Finding 1's perf gate, so it can't be assumed valid once
  // reactivated). Cleared only right after a full rebuild. Starts true: nothing
  // has been built yet. See refreshBands for how this makes toggling session
  // shading back on (or switching back to an intraday timeframe), without an
  // intervening symbol/timeframe reset, still produce correct bands instead of
  // a stale/empty cache from before shading was turned off.
  private bandsCacheDirty = true;
  // bars.length as of the last refreshBarCaches call — purely a bookkeeping
  // cursor (kept in sync with lastAppliedCount); not itself consulted for any
  // branch decision, which all live on lastBarsOp/appendedFrom above.
  private cachedBarCount = 0;
  // The one calendar ET day whose boundaries are currently cached (sessions.ts).
  // Rebuilt (one Intl call) only when a bar's ms falls outside its window.
  private daySeg: DaySegment | null = null;
  // Count of buildDaySegment (Intl.DateTimeFormat) calls made during the MOST
  // RECENT sync() — reset to 0 at the top of every sync(), incremented in
  // extendBandsFrom whenever the day-segment cache misses. Temporary diagnostic
  // probe (Task 6 of the UI perf plan): lets a real device confirm the cache
  // above is actually amortizing the Intl cost (near-0 in steady state) rather
  // than trusting that by inference. Unconditional, no perf.enabled gate here —
  // incrementing an int is negligible even when nobody reads it; the gate
  // belongs at the read/report site (ChartPanel.tsx), mirroring buildTapeRows'
  // always-returned `scanned` count.
  private daySegmentBuildsThisSync = 0;
  private chartType: ChartType = "candle";
  private showSessions = true;
  private gridVisible = true;
  private volumeVisible = true;
  private watermarkOn = false;
  // Suppressed while ChartPanel is showing its own merged price+countdown badge
  // (BarCloseTimer) so LWC's built-in tag doesn't double up behind it; restored
  // whenever the main series is recreated (setChartType) or restyled (setPalette),
  // since mainSeriesOptions() doesn't know about this override.
  private lastValueVisible = true;
  private readonly indicators = new Map<string, { inst: IndicatorInstance; series: Map<string, LwcSeries> }>();
  // Stretch factor a collapsed pane had before collapsing, so expanding restores it
  // instead of resetting to LWC's default of 1 (which would undo a manual resize).
  private readonly expandedStretchFactor = new Map<number, number>();

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
    this.daySegmentBuildsThisSync = 0;
    const bars = this.deps.bars.series(this.config.symbol, this.config.timeframe);
    this.applyBars(bars);
    this.refreshBarCaches(bars);
    this.applyIndicators();
    this.applySessions(bars);
  }

  private applyBars(bars: Bar[]): void {
    if (bars.length === 0) return; // cold symbol — panel shows the hint, not an error
    if (!this.backfilled) {
      this.setAllBars(bars);
      return;
    }
    // The incremental branches below assume the drawn candles are a PREFIX of `bars`
    // that only extends at the tail. A BarStore snapshot replace — the deep-history
    // backfill landing after the shallow live seed prepends ~20 days of 1m bars; the
    // official daily series replacing the single derived in-progress day — grows the
    // series at the FRONT, so the bar now at the anchor index is a different, older
    // bar. Replaying update() from there hands LWC a time older than it holds
    // ("Cannot update oldest data") and never draws the prepended history. Rebuild
    // wholesale instead. Mirrors applyIndicators' `continues` guard.
    const anchor = bars[this.lastAppliedCount - 1];
    if (!anchor || anchor.bucketStart !== this.lastTailBucket) {
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
      this.lastTailBucket = last.bucketStart;
      this.lastBarsOp = "appended";
      this.appendedFrom = from;
    } else if (lastChanged) {
      this.candle.update(this.mainPoint(last));
      this.volume.update(toVolume(last, this.palette));
      this.lastAppliedKey = keyOf(last);
      this.lastTailBucket = last.bucketStart;
      // Auto-follow is LWC's default when already at the right edge; never force it
      // when the user has scrolled back (honesty: don't yank their view).
      this.lastBarsOp = "tailUpdated";
    } else {
      this.lastBarsOp = "none";
    }
  }

  private setAllBars(bars: Bar[]): void {
    // Captured BEFORE setData: LWC's setData preserves the viewport's LOGICAL
    // index range, not its TIME range. A front-growth rebuild (deep-history
    // prepend, or the official-daily-replaces-derived-bar case) shifts every
    // existing bar's logical index, so without restoring below, a user scrolled
    // back would have their viewport silently teleport to a different time
    // window on every prepend.
    const before = this.facade.getVisibleRange();
    const pad = this.leftPad(bars);
    this.candle.setData([...pad, ...bars.map((b) => this.mainPoint(b))]);
    this.volume.setData([...pad, ...bars.map((b) => toVolume(b, this.palette))]);
    // Restore the pre-rebuild time window — unless the user was parked at the
    // right/live edge, where LWC's own follow-live behavior is already correct
    // and must not be overridden into a stale range.
    if (before && bars.length > 0) {
      const newestSec = toLwcTime(bars[bars.length - 1].bucketStart);
      const atRightEdge = before.to >= newestSec;
      if (!atRightEdge) this.facade.setVisibleRange(before);
    }
    this.backfilled = true;
    // lastAppliedCount/lastAppliedKey/lastTailBucket track the REAL bars only — the
    // incremental applyBars path above indexes into `bars` (the BarReader's series),
    // which never includes this padding.
    this.lastAppliedCount = bars.length;
    this.lastAppliedKey = keyOf(bars[bars.length - 1]);
    this.lastTailBucket = bars[bars.length - 1].bucketStart;
    // Single source of truth for the "reset" outcome: setAllBars is the only
    // thing all 3 reset call sites in applyBars share, so marking it here (rather
    // than once per call site) can't drift out of sync with a future 4th site.
    this.lastBarsOp = "reset";
  }

  // Read-only mirror of barsMsCache — Date.parse(bucketStart) for every currently-
  // applied bar, index-aligned with the BarReader's series. Lets other call sites
  // (e.g. the drawings primitive) reuse the maintained cache instead of running
  // their own O(bars) .map(Date.parse) on every paint.
  barsMs(): readonly number[] { return this.barsMsCache; }

  // bars.length as of the last refreshBarCaches call — a bookkeeping cursor kept
  // in sync with lastAppliedCount, exposed so tests can assert the caches never
  // silently fall behind the applied series (no branch decision consults it —
  // those all key off lastBarsOp/appendedFrom instead).
  barsCached(): number { return this.cachedBarCount; }

  // Diagnostic-only (Task 6): how many times buildDaySegment's Intl call
  // actually ran during the most recent sync() — ~0 in steady state once the
  // day-segment cache above is being hit, versus roughly one per bar before it
  // existed. Read by ChartPanel, itself guarded behind perf.enabled, and
  // reported to the shared PerfMonitor singleton so a live re-measurement can
  // prove the fix rather than infer it from paint duration alone.
  lastSyncDaySegmentBuilds(): number { return this.daySegmentBuildsThisSync; }

  // Refreshes barsMsCache/bandsCache/closedRange (and, from those, candleRange)
  // to match `bars` exactly, sharing lastBarsOp/appendedFrom (just set by
  // applyBars, above) so every cache advances from the SAME notion of "what's
  // new" as the LWC replay did — never a second, independently-computed cursor.
  //
  // Contract (holds after this returns, for every reset/appended/tailUpdated/none
  // sequence): barsMsCache deep-equals bars.map(b => Date.parse(b.bucketStart));
  // closedRange equals candleRangeOf(bars.slice(0, -1)); bandsCache deep-equals
  // bandsFromBars(bars) -- but ONLY while sessions are active (see refreshBands'
  // gate, Finding 1). barsMsCache/closedRange (hence candleRange) stay
  // unconditional regardless — drawings projection and overlay-indicator
  // autoscale need them on every timeframe, not just when shading is on. See
  // ChartController.test.ts's equivalence tests.
  private refreshBarCaches(bars: Bar[]): void {
    if (bars.length === 0) return; // nothing to cache; resetForReload already cleared everything
    switch (this.lastBarsOp) {
      case "reset":
        this.barsMsCache = bars.map((b) => Date.parse(b.bucketStart));
        this.closedRange = candleRangeOf(bars.slice(0, -1));
        // A reset may have loaded an entirely different series (new symbol/
        // timeframe, or a front-growth rebuild) — whatever bandsCache/dirty
        // state carried over from before is meaningless against it. Force
        // refreshBands to do a full from-scratch rebuild below, unconditionally
        // (not just when the PREVIOUS state happened to be dirty already).
        this.bandsCacheDirty = true;
        break;
      case "appended": {
        const from = this.appendedFrom;
        // Re-parse from `from` (not just the genuinely-new tail): the bar at
        // `from` was `last` as of the previous apply and may have itself changed
        // (e.g. finalized) during the same missed window that produced the new
        // bars — mirrors applyBars' own re-flush-from-`from` rationale. bucketStart
        // is immutable per bar identity, so this re-parse is a no-op value-wise
        // when unchanged, and necessary when the bar object was swapped for a
        // revised one anyway (harmless either way).
        for (let i = from; i < bars.length; i++) {
          const ms = Date.parse(bars[i].bucketStart);
          if (i < this.barsMsCache.length) this.barsMsCache[i] = ms;
          else this.barsMsCache.push(ms);
        }
        this.foldClosedRangeFrom(from, bars);
        break;
      }
      case "tailUpdated":
      case "none":
        // Every existing bar's bucketStart (hence its ms and session) is
        // unchanged; closedRange excludes the live last bar so a tail-only
        // revision never invalidates it either — nothing to refresh.
        break;
    }
    this.refreshBands(bars);
    this.cachedBarCount = bars.length;
    // candleRange = closedRange (everything but the last bar) folded with the
    // CURRENT last bar's own l/h, read fresh every call — so an in-progress bar
    // that spikes then retreats is always reflected exactly, never stuck at
    // whatever its highest-seen high was on some earlier call.
    const last = bars[bars.length - 1];
    this.candleRange = combine(this.closedRange, last.l, last.h);
  }

  // Builds/extends bandsCache — but ONLY when applySessions will actually read
  // it (same gate it uses: an intraday timeframe with session shading on). On a
  // Daily chart with years of history, or an intraday chart with shading
  // manually switched off, applySessions immediately discards bandsCache in
  // favor of an empty array — so maintaining it on every reset/appended sync
  // (each bar potentially costing an Intl.DateTimeFormat call via
  // buildDaySegment) was pure waste. Finding 1 of the follow-up review.
  //
  // bandsCacheDirty tracks whether the cache is trustworthy for a full-history
  // read: set on every "reset" (a different series may now be loaded) and
  // whenever sessions are inactive (maintenance is paused while unused, so a
  // stale/short cache from before deactivation can't be assumed valid once
  // reactivated). Consulted here, not just written: whenever sessions ARE
  // active and the cache is dirty, this rebuilds from scratch over the FULL
  // `bars` regardless of lastBarsOp — covering "reset" (needs a full rebuild
  // anyway) AND, crucially, session shading (or the timeframe) having just been
  // switched back on with no bar change at all (lastBarsOp "tailUpdated"/"none")
  // — the toggle-back-on scenario this gate must not regress. Only once the
  // cache is known-fresh does an "appended" sync fall back to the cheaper
  // incremental extend.
  private refreshBands(bars: Bar[]): void {
    const sessionsActive = !["D", "W", "M"].includes(this.config.timeframe) && this.showSessions;
    if (!sessionsActive) { this.bandsCacheDirty = true; return; }
    if (this.bandsCacheDirty) {
      this.daySeg = null;
      this.bandsCache = [];
      this.extendBandsFrom(0, bars);
      this.bandsCacheDirty = false;
      return;
    }
    if (this.lastBarsOp === "appended") this.extendBandsFrom(this.appendedFrom, bars);
    // tailUpdated/none: every existing bar's bucketStart (hence its session) is
    // unchanged — nothing to extend.
  }

  // Extends bandsCache (assumed to already correctly cover bars[0 .. from-1], or
  // to be empty when from === 0) through bars[from .. bars.length-1]. Mirrors
  // bandsFromBars' exact run-detection/edge semantics — see its comment — one
  // bar at a time using the already-parsed barsMsCache instead of re-parsing.
  private extendBandsFrom(from: number, bars: Bar[]): void {
    for (let i = from; i < bars.length; i++) {
      const ms = this.barsMsCache[i];
      if (!this.daySeg || ms < this.daySeg.dayStartMs || ms >= this.daySeg.dayEndMs) {
        this.daySeg = buildDaySegment(ms);
        this.daySegmentBuildsThisSync++;
      }
      const session = classify(ms, this.daySeg);
      const cur = this.bandsCache[this.bandsCache.length - 1];
      if (!cur || cur.session !== session) {
        if (cur) cur.endMs = ms; // close the previous run at this bar
        this.bandsCache.push({ startMs: ms, endMs: ms, session });
      }
      // else: still inside the same run — nothing to record per-bar, only run
      // boundaries are stored (mirrors bandsFromBars' `continue`).
    }
    // The final band's end is the LAST bar's own time, not lastBar+span (see
    // bandsFromBars) — reasserted every call so a same-session bar that merely
    // extends the current run still advances the open band's end.
    if (bars.length > 0) {
      const lastBand = this.bandsCache[this.bandsCache.length - 1];
      if (lastBand) lastBand.endMs = this.barsMsCache[bars.length - 1];
    }
  }

  // Folds bars[from .. bars.length-2] (i.e. every bar in that span EXCEPT the
  // current last one, which is live/in-progress and excluded from closedRange by
  // definition) into closedRange. `from` is applyBars' own appendedFrom, so this
  // always includes the previously-last bar — which may have just finalized in
  // the same missed window that also appended new bars, and so may be entering
  // closedRange for the first time here.
  private foldClosedRangeFrom(from: number, bars: Bar[]): void {
    const base = this.closedRange ?? { minValue: Infinity, maxValue: -Infinity };
    let { minValue, maxValue } = base;
    for (let i = from; i <= bars.length - 2; i++) {
      if (bars[i].l < minValue) minValue = bars[i].l;
      if (bars[i].h > maxValue) maxValue = bars[i].h;
    }
    this.closedRange = { minValue, maxValue };
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
        // The store is keyed purely by instanceId, not (instanceId, symbol, timeframe)
        // — a rapid re-subscribe (e.g. clicking 1m/5m repeatedly) can land a snapshot
        // for a whole different bucket grid while `applied` still reflects the
        // previous timeframe's count. A same-or-greater length is then just a
        // coincidence, not a real continuation, so verify the point already sitting
        // at index (applied-1) is still THAT point (by time, ignoring value so an
        // in-progress revision doesn't trip this) before trusting update()'s
        // append-in-place. LWC's update() throws ("Cannot update oldest data") on a
        // time that goes backwards relative to what it already has — and a painter
        // that throws MAX_CONSECUTIVE_FAILURES times in a row (Scheduler) gets its
        // whole chart torn down, not just this series, which is why a rapid-switch
        // session used to eventually lose the candles too.
        const continues = applied > 0 && points.length >= applied
          && points[applied - 1]?.timeMs === this.indicatorLastAppliedTimeMs.get(d.key);
        if (applied === 0 || points.length < applied || !continues) {
          // First application, the series shrank (e.g. a full recompute produced
          // fewer points), or the store handed back a different generation —
          // only setData() is safe.
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
        if (last) this.indicatorLastAppliedTimeMs.set(d.key, last.timeMs);
        else this.indicatorLastAppliedTimeMs.delete(d.key);
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
    if (!intraday || bars.length === 0 || !this.showSessions) { this.facade.setSessionBands([]); return; }
    // bandsCache is refreshed (by refreshBarCaches, from sync()) to always match
    // what bandsFromBars(bars) would produce from scratch — see the equivalence
    // tests in ChartController.test.ts. bandsFromBars itself is kept below,
    // unused by production code now, as the from-scratch reference those tests
    // compare against.
    this.facade.setSessionBands(this.bandsCache);
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
          // No highlighted last-value box on the price axis either — only the candle
          // (the main series, set up separately via candleOptions) keeps that.
          lastValueVisible: false,
          visible: !(resolved.hidden ?? false) && !d.hidden && !(resolved.collapsed ?? false),
          // No crosshair dot riding the study lines (TV doesn't draw one for
          // overlay indicators; the crosshair itself is free-moving — chartTheme).
          ...(d.kind === "line" ? { lineWidth: d.width, lineStyle: LWC_LINE_STYLE[d.lineStyle], crosshairMarkerVisible: false } : {}),
          // Main-pane overlay lines (EMA/SMA/VWAP) share the candle price scale, bounded
          // to OVERLAY_AUTOSCALE_FACTORx the live candle range (chartTheme's
          // boundedOverlayAutoscale) so a far-off value stays visible without crushing
          // the candles. MACD's sub-pane lines (paneIndex 1) are excluded: they must
          // autoscale their own pane.
          ...(d.kind === "line" && d.paneIndex === 0
            ? { autoscaleInfoProvider: boundedOverlayAutoscale(() => this.candleRange, OVERLAY_AUTOSCALE_FACTOR) }
            : {}),
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
      this.indicatorLastAppliedTimeMs.delete(k);
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
    const collapsed = next.collapsed ?? false;
    for (const d of describeIndicator(next, this.palette)) {
      existing.series.get(d.key)?.applyOptions({
        color: d.color,
        visible: !hidden && !d.hidden && !collapsed,
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
    this.lastTailBucket = "";
    this.candleRange = null;
    this.closedRange = null;
    this.barsMsCache = [];
    this.bandsCache = [];
    this.cachedBarCount = 0;
    this.daySeg = null;
    this.lastBarsOp = "reset";
    this.appendedFrom = 0;
    this.indicatorApplied.clear();
    this.indicatorLastKey.clear();
    this.indicatorLastAppliedTimeMs.clear();
    // Wipe the previous (symbol, timeframe)'s bars immediately — otherwise a
    // switch to a series that's empty or slow to arrive (e.g. Daily -> a cold
    // 1m symbol) leaves the old timeframe's candles frozen on screen forever
    // (applyBars early-returns on an empty series, so it would never clear them).
    this.candle.setData([]);
    this.volume.setData([]);
    this.facade.setSessionBands([]);
    // Wipe the previous symbol's overlay/study data too. Otherwise each indicator's
    // LWC series AND its shared-store entry (keyed by instanceId, not symbol) keep the
    // OLD symbol's points drawn until the engine's fresh snapshot arrives — a stale,
    // differently-priced VWAP/EMA/SMA line then drags the shared price scale down on
    // the next reset-view / jump-to-live (down-spike + 0-based autoscale). Clearing
    // both also keeps indicatorApplied at 0 (already cleared above) so the incoming
    // snapshot takes the clean setData() branch instead of applyIndicators' continues()
    // last-point-only update.
    for (const { series } of this.indicators.values()) {
      for (const [key, s] of series) {
        this.deps.indicators.reset(key);
        s.setData([]);
      }
    }
    // Re-subscribe every live indicator for the new (symbol, timeframe).
    for (const { inst } of this.indicators.values()) this.subscribeIndicator(inst);
    if (this.watermarkOn) this.facade.setWatermark(bareSymbol(this.config.symbol));
  }

  setPalette(p: Palette): void {
    this.palette = p;
    this.facade.applyOptions(chartOptions(p));
    this.candle.applyOptions(mainSeriesOptions(this.chartType, p));
    this.candle.applyOptions({ lastValueVisible: this.lastValueVisible });
    this.volume.applyOptions({ ...volumeOptions(p), visible: this.volumeVisible });
    for (const { inst, series } of this.indicators.values())
      for (const d of describeIndicator(inst, p)) series.get(d.key)?.applyOptions({ color: d.color });
    this.applyGrid();
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
    this.candle.applyOptions({ lastValueVisible: this.lastValueVisible });
    // Force a full re-seed of the new series on the next sync().
    this.backfilled = false;
    this.lastAppliedCount = 0;
    this.lastAppliedKey = "";
    this.lastTailBucket = "";
    this.closedRange = null;
    this.barsMsCache = [];
    this.bandsCache = [];
    this.cachedBarCount = 0;
    this.daySeg = null;
    this.lastBarsOp = "reset";
    this.appendedFrom = 0;
    this.liftCandleToTop();
  }

  setFills(markers: FillMarker[]): void { this.facade.setFillMarkers(markers); }
  setShowSessions(on: boolean): void { this.showSessions = on; }
  setGrid(on: boolean): void { this.gridVisible = on; this.applyGrid(); }
  setVolumeVisible(on: boolean): void { this.volumeVisible = on; this.volume.applyOptions({ visible: on }); }
  setLastValueVisible(on: boolean): void { this.lastValueVisible = on; this.candle.applyOptions({ lastValueVisible: on }); }
  setWatermark(on: boolean): void { this.watermarkOn = on; this.facade.setWatermark(on ? bareSymbol(this.config.symbol) : null); }

  private applyGrid(): void {
    this.facade.applyOptions({ grid: { vertLines: { visible: this.gridVisible }, horzLines: { visible: this.gridVisible } } });
  }

  // Collapse a sub-pane (e.g. MACD) to a thin strip, or restore its prior size.
  // Collapsing remembers the current stretch factor only if it's not already at/below
  // the collapsed floor, so repeated collapse calls don't overwrite the remembered
  // expanded size with the collapsed one.
  //
  // Collapsing also hides every series living in that pane — only the (DOM, separate)
  // legend stays visible, per the "collapse should hide the drawing" behavior — and
  // restores each series to its normal (hidden/per-slot-hidden-aware) visibility on
  // expand. `entry.inst.collapsed` is kept in sync so a later updateIndicator (e.g. a
  // style-only edit made while collapsed) doesn't accidentally re-show it.
  setPaneCollapsed(paneIndex: number, collapsed: boolean): void {
    if (collapsed) {
      const cur = this.facade.paneStretchFactor(paneIndex);
      if (cur > COLLAPSED_STRETCH) this.expandedStretchFactor.set(paneIndex, cur);
      this.facade.setPaneStretchFactor(paneIndex, COLLAPSED_STRETCH);
    } else {
      this.facade.setPaneStretchFactor(paneIndex, this.expandedStretchFactor.get(paneIndex) ?? 1);
    }
    for (const entry of this.indicators.values()) {
      let inPane = false;
      for (const d of describeIndicator(entry.inst, this.palette)) {
        if (d.paneIndex !== paneIndex) continue;
        inPane = true;
        const hidden = entry.inst.hidden ?? false;
        entry.series.get(d.key)?.applyOptions({ visible: !hidden && !d.hidden && !collapsed });
      }
      if (inPane) entry.inst = { ...entry.inst, collapsed };
    }
  }

  resize(w: number, h: number): void { this.facade.resize(w, h); }
  jumpToLive(): void { this.facade.scrollToRealTime(); }
  resetZoom(): void { this.facade.resetTimeScale(); this.facade.resetPriceScale(); }
  dispose(): void {
    for (const id of [...this.indicators.keys()]) this.removeIndicator(id);
    this.facade.remove();
  }
}

function keyOf(b: Bar): string { return `${b.bucketStart}|${b.c}|${b.h}|${b.l}|${b.v}|${b.inProgress}`; }
function bareSymbol(s: string): string { return s.replace(/^US\./, ""); }
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
// [low, high] across every currently-applied bar — the reference range overlay
// lines are bounded against. A plain scan: bar counts here (a few thousand at
// most) make this negligible next to the rest of applyBars' per-sync work.
// Exported (unused by production code — refreshBarCaches maintains candleRange
// incrementally instead) purely as the from-scratch reference ChartController.
// test.ts's equivalence tests assert `closedRange`/`candleRange` against.
export function candleRangeOf(bars: Bar[]): PriceRange {
  let minValue = Infinity, maxValue = -Infinity;
  for (const b of bars) {
    if (b.l < minValue) minValue = b.l;
    if (b.h > maxValue) maxValue = b.h;
  }
  return { minValue, maxValue };
}
// Folds one more bar's [l, h] into a PriceRange (or the identity range when
// `range` is null) — the O(1) step refreshBarCaches uses to combine closedRange
// with the CURRENT last bar's own l/h on every sync() call.
function combine(range: PriceRange | null, l: number, h: number): PriceRange {
  return {
    minValue: Math.min(range?.minValue ?? Infinity, l),
    maxValue: Math.max(range?.maxValue ?? -Infinity, h),
  };
}
function toVolume(b: Bar, p: Palette) {
  return { time: toLwcTime(b.bucketStart), value: b.v, color: b.c >= b.o ? p.volUp : p.volDown };
}

// One band per contiguous run of same-session bars, with every edge pinned to a
// real bar's bucketStart (see the applySessions comment above for why: the
// session primitive drops a band whose edge doesn't land on an exact bar time).
// The final band's end is the LAST bar's own time, not lastBar+span — extending
// past the last bar would reintroduce the same null-coordinate problem this
// function exists to avoid.
// Exported (unused by production code — applySessions reads the incrementally-
// maintained bandsCache instead) purely as the from-scratch reference
// ChartController.test.ts's equivalence tests assert bandsCache against.
export function bandsFromBars(bars: Bar[]): Band[] {
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
