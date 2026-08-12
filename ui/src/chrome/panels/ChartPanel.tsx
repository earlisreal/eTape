import { useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { createChart, createTextWatermark, CandlestickSeries, BarSeries, HistogramSeries, LineSeries, AreaSeries, type IChartApi, type ISeriesApi, type Time, type Logical, type LogicalRange, type Coordinate } from "lightweight-charts";
import type { PanelProps } from "./registry";
import { ChartController, type ManagedViewportMode } from "../../render/chart/ChartController";
import { clampRightScroll, RIGHT_OFFSET_BARS, usesBoundaryManagedFollow, type ChartType } from "../../render/chart/chartTheme";
import type { ChartApiFacade, LwcSeries } from "../../render/chart/ChartApiFacade";
import { DiamondFillPrimitive } from "../../render/chart/diamondPrimitive";
import { SessionShadingPrimitive } from "../../render/chart/sessionPrimitive";
import { INDICATOR_CATALOG, withDefaultParams, describeIndicator, type IndicatorInstance, type IndicatorType } from "../../render/chart/indicatorSeries";
import { DrawingsPrimitive } from "../../render/chart/drawings/primitive";
import { DrawingInteraction, type Tool } from "../../render/chart/drawings/interaction";
import { timeframeToMs } from "../../render/chart/drawings/geometry";
import { bucketStartMs, type Timeframe } from "../../render/chart/barBucket";
import { aggregateFillMarkers } from "../../render/chart/fillAggregate";
import { isIntradayTimeframe, latestEligibleCountdownBar } from "../../render/chart/barClose";
import { formatPrice } from "../../render/format";
import type { Palette } from "../../render/palette";
import { useTheme } from "../ThemeProvider";
import type { Drawing } from "../../render/chart/drawings/model";
import type { LineStyleName } from "../../render/chart/lineStyle";
import { getTvPalette, getTvChrome } from "../../render/chart/tvTheme";
import { PanelHeaderSlotContext } from "./headerSlot";
import { ChartHeaderControls } from "./tv/ChartHeaderControls";
import { TVDrawingRail, type RailPos } from "./tv/TVDrawingRail";
import { TVContextMenu, type MenuEntry } from "./tv/TVContextMenu";
import { TVLegend, type TVLegendHandle } from "./tv/TVLegend";
import { TVFloatingToolbar } from "./tv/TVFloatingToolbar";
import { IndicatorSettingsDialog } from "./tv/IndicatorSettingsDialog";
import { ChartSettingsDialog, DEFAULT_CHART_SETTINGS, type ChartSettings } from "./tv/ChartSettingsDialog";
import { computeLegendView } from "./tv/legendView";
import { BarCloseTimer } from "./tv/BarCloseTimer";
import { perf } from "../../perf/PerfMonitor";
import { bareSymbol } from "../exec/orderStatus";
import type { QueryChartWindowResult } from "../../gen/wsmsg";
import { uiLog } from "../../logging/logger";

const ALL_CHART_BARS = 1_000_000;

type PendingIndicatorHydration = {
  instanceId: string;
  seriesKeys: string[];
  readyDebt: number;
  generation: number;
  querying: boolean;
  retryUsed: boolean;
};

// Adapts a real LWC v5 IChartApi to the controller's minimal ChartApiFacade.
function makeFacade(chart: IChartApi, palette: Palette): {
  facade: ChartApiFacade; setPalette: (p: Palette) => void; drawings: DrawingsPrimitive;
} {
  let main: ISeriesApi<"Candlestick" | "Bar" | "Line" | "Area"> | null = null;
  let sessionAttached = false;
  let watermark: { detach: () => void } | null = null;
  const session = new SessionShadingPrimitive(palette);
  const diamonds = new DiamondFillPrimitive(palette);
  const drawings = new DrawingsPrimitive(palette);

  const facade: ChartApiFacade = {
    setMainSeries: (kind, options) => {
      if (main) chart.removeSeries(main);
      // Per-branch addSeries calls (NOT a hoisted `ctor` variable): LWC v5's addSeries
      // is generic on the concrete SeriesDefinition, so the constructor must appear at
      // the call site to type-check — the same reason the pre-existing addSeries below
      // uses a per-branch ternary.
      const s = kind === "candle" ? chart.addSeries(CandlestickSeries, options as object, 0)
        : kind === "bar" ? chart.addSeries(BarSeries, options as object, 0)
        : kind === "line" ? chart.addSeries(LineSeries, options as object, 0)
        : chart.addSeries(AreaSeries, options as object, 0);
      main = s as ISeriesApi<"Candlestick">;
      // The diamond + drawings series-primitives ride the main price series so
      // they survive a chart-type swap; the session pane-primitive attaches once.
      main.attachPrimitive(diamonds);
      main.attachPrimitive(drawings);
      if (!sessionAttached) { chart.panes()[0]?.attachPrimitive?.(session); sessionAttached = true; }
      return s as unknown as LwcSeries;
    },
    addSeries: (kind, options, paneIndex) => {
      const s = kind === "line" ? chart.addSeries(LineSeries, options as object, paneIndex)
        : chart.addSeries(HistogramSeries, options as object, paneIndex);
      return s as unknown as LwcSeries;
    },
    removeSeries: (s) => chart.removeSeries(s as unknown as ISeriesApi<"Line">),
    setPriceScaleMargins: (id, margins) => chart.priceScale(id).applyOptions({ scaleMargins: margins }),
    setSessionBands: (bands) => session.setBands(bands),
    setFillMarkers: (m) => diamonds.setMarkers(m),
    timeToCoordinate: (ms) => chart.timeScale().timeToCoordinate((Math.floor(ms / 1000)) as unknown as Time),
    priceToCoordinate: (price) => main?.priceToCoordinate(price) ?? null,
    logicalToCoordinate: (logical) => chart.timeScale().logicalToCoordinate(logical as Logical),
    coordinateToLogical: (x) => chart.timeScale().coordinateToLogical(x as Coordinate),
    coordinateToPrice: (y) => main?.coordinateToPrice(y as Coordinate) ?? null,
    setPanZoomEnabled: (on) => chart.applyOptions({ handleScroll: on, handleScale: on }),
    scrollToRealTime: () => chart.timeScale().scrollToPosition(RIGHT_OFFSET_BARS, false),
    getScrollPosition: () => chart.timeScale().scrollPosition(),
    resetTimeScale: () => chart.timeScale().resetTimeScale(),
    resetPriceScale: () => chart.priceScale("right").applyOptions({ autoScale: true }),
    getVisibleRange: () => {
      const r = chart.timeScale().getVisibleRange();
      return r ? { from: r.from as unknown as number, to: r.to as unknown as number } : null;
    },
    setVisibleRange: (range) =>
      chart.timeScale().setVisibleRange({ from: range.from as unknown as Time, to: range.to as unknown as Time }),
    getVisibleLogicalRange: () => {
      const r = chart.timeScale().getVisibleLogicalRange();
      return r ? { from: r.from, to: r.to } : null;
    },
    setVisibleLogicalRange: (range) =>
      chart.timeScale().setVisibleLogicalRange({ from: range.from as Logical, to: range.to as Logical }),
    resize: (w, h) => chart.resize(w, h),
    applyOptions: (o) => chart.applyOptions(o as object),
    setWatermark: (text) => {
      if (watermark) { watermark.detach(); watermark = null; }
      if (text) {
        const pane = chart.panes()[0];
        if (pane) watermark = createTextWatermark(pane, { horzAlign: "center", vertAlign: "center",
          lines: [{ text, color: "rgba(120,123,134,.18)", fontSize: 48, fontStyle: "bold" }] });
      }
    },
    takeScreenshot: () => chart.takeScreenshot(),
    subscribeCrosshairMove: (cb) => {
      const handler = (param: { logical?: number }) => cb(typeof param.logical === "number" ? param.logical : null);
      chart.subscribeCrosshairMove(handler);
      return () => chart.unsubscribeCrosshairMove(handler);
    },
    paneHeights: () => chart.panes().map((pn) => pn.getHeight()),
    paneStretchFactor: (i) => chart.panes()[i]?.getStretchFactor() ?? 1,
    setPaneStretchFactor: (i, f) => chart.panes()[i]?.setStretchFactor(f),
    priceScaleWidth: () => chart.priceScale("right").width(),
    remove: () => chart.remove(),
  };
  return { facade, setPalette: (p) => { session.setPalette(p); diamonds.setPalette(p); drawings.setPalette(p); }, drawings };
}

export function ChartPanel({ config, stores, scheduler, width, height, linkGroups, commands, onConfigChange, group: groupProp, symbol: symbolProp }: PanelProps): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const controllerRef = useRef<ChartController | null>(null);
  const setFacadePaletteRef = useRef<((p: Palette) => void) | null>(null);
  const idSeq = useRef(0);
  // appPalette is the app-wide Daylight-Ledger palette (same one PanelFrame/TopBar
  // use) — for ChartHeaderControls, which portals into the ledger header and must
  // match its chrome. `palette` below stays the TV-faithful canvas palette the chart
  // itself (candles, primitives, ChartController) has always used.
  const { mode, palette: appPalette } = useTheme();
  const palette = getTvPalette(mode);
  const chrome = getTvChrome(mode);
  const headerSlot = useContext(PanelHeaderSlotContext);
  const symbol = symbolProp ?? (config.settings.symbol as string) ?? "US.AAPL";
  const timeframe0 = (config.settings.timeframe as string) ?? "1m";
  const chartType0 = (config.settings.chartType as ChartType) ?? "candle";
  const hideAll0 = (config.settings.hideAllDrawings as boolean) ?? false;
  const railPos0 = (config.settings.drawingRailPos as RailPos | undefined) ?? null;
  const drawingToolsVisible0 = (config.settings.drawingToolsVisible as boolean | undefined) ?? true;
  const chartSettings0: ChartSettings = { ...DEFAULT_CHART_SETTINGS, ...((config.settings.chartSettings as Partial<ChartSettings>) ?? {}) };
  // config.group is frozen (dockview captures this panel's factory once, at
  // creation, and never re-invokes it with a fresh config on a later group
  // re-pick — see PanelFrame's `group` prop comment). PanelFrame threads its own
  // live `group` state through as a prop; fall back to config.group so tests
  // that construct PanelProps directly (no `group` prop) keep working.
  const group = groupProp === undefined ? config.group : groupProp;

  // Config surfaces (timeframe + indicators) ARE low-rate chrome, so React state is
  // fine here (the hard rule is about market data, not per-chart config).
  const [timeframe, setTf] = useState(timeframe0);
  // Drop any persisted instance whose type no longer exists in the catalog
  // (e.g. a workspace saved before the DELTA indicator was retired) — an
  // unknown type would otherwise crash describeIndicator/withDefaultParams.
  const [instances, setInstances] = useState<IndicatorInstance[]>(
    ((config.settings.indicators as IndicatorInstance[]) ?? []).filter(
      (i) => INDICATOR_CATALOG[i.type as IndicatorType] !== undefined,
    ),
  );

  const interactionRef = useRef<DrawingInteraction | null>(null);
  const tfRef = useRef<string>(timeframe0);
  // Timeframe/symbol switches clear the controller's series synchronously but
  // bump no store revision, so the scheduler's revision-based isDirty() would
  // otherwise never repaint them until an unrelated bar delta happens to
  // arrive. This flag forces exactly one repaint on the next scheduled frame.
  const forceRepaintRef = useRef(false);
  const viewportModeRef = useRef<ManagedViewportMode>("live");
  const [activeTool, setActiveTool] = useState<Tool>("select");
  const [chartSymbol, setChartSymbol] = useState(symbol);
  const [menu, setMenu] = useState<{ x: number; y: number; clientX: number; clientY: number; drawingId: string | null } | null>(null);
  // The top-bar chart-type switcher was removed (candles-only trading UI); the
  // persisted setting is still honored at mount so old workspaces keep rendering.
  const chartType = chartType0;
  const [hideAll, setHideAll] = useState(hideAll0);
  const [drawingToolsVisible, setDrawingToolsVisible] = useState(drawingToolsVisible0);
  const [drawingRailPos, setDrawingRailPos] = useState<RailPos | null>(railPos0);
  const [chartSettings, setChartSettings] = useState<ChartSettings>(chartSettings0);
  const [settingsInstanceId, setSettingsInstanceId] = useState<string | null>(null);
  const [chartSettingsOpen, setChartSettingsOpen] = useState(false);
  const [paneOffsets, setPaneOffsets] = useState<number[]>([0]);
  const [rightAxisWidth, setRightAxisWidth] = useState(0);
  // Position + direction + value of the in-progress bar's live price, used to
  // anchor and label BarCloseTimer's merged price+countdown badge — null while
  // there's no in-progress bar (nothing live to show) or off-screen.
  const [lastPriceTag, setLastPriceTag] = useState<{ y: number; up: boolean; price: number } | null>(null);
  const [selection, setSelection] = useState<{ id: string; rect: { x: number; y: number; w: number; h: number }; color: string; width: number; lineStyle: LineStyleName } | null>(null);

  const legendRef = useRef<TVLegendHandle | null>(null);
  const instancesRef = useRef(instances);
  const paletteRef = useRef(palette);
  const pendingIndicatorHydrationRef = useRef<Map<string, PendingIndicatorHydration>>(new Map());
  const chartGenerationRef = useRef(0);
  const crosshairLogicalRef = useRef<number | null>(null);
  const refreshSelRef = useRef<() => void>(() => {});
  const facadeRef = useRef<ChartApiFacade | null>(null);
  const drawingsPrimRef = useRef<DrawingsPrimitive | null>(null);

  useEffect(() => { tfRef.current = timeframe; }, [timeframe]);

  // The mount effect below is [config.id]-only (the chart/canvas must never
  // remount on a symbol/group/timeframe change — see that effect's closing
  // comment), so it captures `group` at mount time. groupRef lets the reactive
  // effect further down (which DOES see live `group` changes) tell the
  // already-mounted closure "the group was reassigned, re-resolve the symbol" —
  // applySymbolRef is that closure's own applySymbol, captured once it's created.
  const groupRef = useRef(group);
  const ownSymbolRef = useRef(symbol);
  const applySymbolRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    viewportModeRef.current = "live";
    const chart = createChart(host, { width, height });
    // Right-edge pan cap: LWC has no native "capped but non-zero" right-edge option
    // (fixRightEdge hardcodes the margin to 0 — see chartTheme's rightOffset comment),
    // so bound it here. TradingView-style: the user can pan right until the latest
    // bar reaches the left edge of the viewport, not just past the resting margin —
    // clampRightScroll derives that limit from the current visible bar count (the
    // LogicalRange width), so it tracks zoom level. scrollPosition() is the distance
    // in bars from the right edge to the latest bar; snapping it back (without
    // changing bar spacing) preserves zoom. The re-fired event after scrollToPosition
    // is a no-op second pass since scrollPosition() then equals the cap.
    const timeScale = chart.timeScale();
    // subscribeVisibleLogicalRangeChange fires synchronously from LWC's native
    // pan/zoom/wheel handling -- at input-device polling rate (125-1000Hz),
    // not the rAF-gated Scheduler this panel otherwise paints through (see
    // the register() call below). clampRightScroll+scrollToPosition must run
    // synchronously (it's the actual scroll clamp), but refreshSelection --
    // which, when a drawing is selected, re-projects its anchors via a full
    // O(bars) Date.parse scan (DrawingInteraction.selectedRect -> project ->
    // barsMs) -- doesn't need to run more than once per frame. Deferring it
    // (and dropping duplicate frames already pending) fixed chart-pan lag
    // that a monitor 200Hz->60Hz change did NOT fix, confirming the cost was
    // per-native-event, not per-refresh.
    let selectionFrame: number | null = null;
    const scheduleRefreshSelection = () => {
      if (selectionFrame !== null) return;
      selectionFrame = requestAnimationFrame(() => { selectionFrame = null; refreshSelRef.current?.(); });
    };
    const { facade, setPalette, drawings } = makeFacade(chart, palette);
    let viewportGeneration = 0;
    let indicatorReloadPending = false;
    let chartSnapshotLoaded = false;
    let chartSnapshotPending = false;
    let disposed = false;

    const indicatorKeys = () => instancesRef.current.flatMap((inst) => describeIndicator(inst, paletteRef.current).map((series) => series.key));
    const queryIndicatorHydration = async (instanceId: string) => {
      const pending = pendingIndicatorHydrationRef.current.get(instanceId);
      if (!pending || pending.readyDebt !== 0 || pending.querying || !chartSnapshotLoaded) return;
      if (pending.generation !== chartGenerationRef.current) {
        pendingIndicatorHydrationRef.current.delete(instanceId);
        return;
      }
      const generation = chartGenerationRef.current;
      const symbol = currentSymbol;
      const timeframe = tfRef.current;
      const bars = stores.bars.series(symbol, timeframe);
      if (bars.length === 0) return;
      const fromMs = Date.parse(bars[0].bucketStart);
      const toMs = Date.parse(bars[bars.length - 1].bucketStart) + 1;
      if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || fromMs >= toMs) return;

      pending.querying = true;
      let retry = false;
      try {
        const result = await commands.sendQuery("QueryChartWindow", {
          symbol, timeframe, fromMs, toMs, tailBars: 0,
          indicatorSeriesKeys: pending.seriesKeys, skipBars: true,
        }) as QueryChartWindowResult;
        if (disposed || generation !== chartGenerationRef.current || currentSymbol !== symbol || tfRef.current !== timeframe
          || pendingIndicatorHydrationRef.current.get(instanceId) !== pending) return;
        for (const series of result.indicators ?? []) {
          stores.indicators.mergeWindow(series.seriesKey, series.points ?? [], result.fromMs, result.toMs);
          stores.indicators.expandWindow(series.seriesKey, result.fromMs, Number.POSITIVE_INFINITY);
        }
        forceRepaintRef.current = true;
        pendingIndicatorHydrationRef.current.delete(instanceId);
      } catch {
        // WsClient rejects queries that were sent just before a disconnect. Re-issue
        // once while reconnecting so its outbox can carry the hydration request.
        if (pendingIndicatorHydrationRef.current.get(instanceId) === pending && !pending.retryUsed) {
          pending.retryUsed = true;
          retry = true;
        }
      } finally {
        if (pendingIndicatorHydrationRef.current.get(instanceId) === pending) pending.querying = false;
      }
      if (retry) void queryIndicatorHydration(instanceId);
    };
    const flushPendingIndicatorHydration = () => {
      if (!chartSnapshotLoaded) return;
      for (const pending of pendingIndicatorHydrationRef.current.values()) {
        if (pending.generation === chartGenerationRef.current && pending.readyDebt === 0) void queryIndicatorHydration(pending.instanceId);
      }
    };
    const mergeSnapshot = (result: QueryChartWindowResult, generation: number) => {
      const accepted = !disposed && generation === viewportGeneration && result.symbol === currentSymbol && result.timeframe === tfRef.current;
      if (!accepted) return false;
      stores.bars.mergeWindow(result.symbol, result.timeframe, result.bars ?? [], result.fromMs, result.toMs);
      for (const series of result.indicators ?? []) stores.indicators.mergeWindow(series.seriesKey, series.points ?? [], result.fromMs, result.toMs);
      forceRepaintRef.current = true;
      return true;
    };
    const querySnapshot = async () => {
	  if (chartSnapshotLoaded || chartSnapshotPending) return;
      chartSnapshotPending = true;
      try {
        const generation = ++viewportGeneration;
        const startedAt = performance.now();
        const keys = indicatorReloadPending ? [] : indicatorKeys();
        const result = await commands.sendQuery("QueryChartWindow", {
          symbol: currentSymbol, timeframe: tfRef.current, fromMs: 0, toMs: 0, tailBars: ALL_CHART_BARS,
          indicatorSeriesKeys: keys,
        }) as QueryChartWindowResult;
        if (mergeSnapshot(result, generation)) {
          stores.bars.expandWindow(result.symbol, result.timeframe, result.fromMs, Number.POSITIVE_INFINITY);
          for (const key of keys) stores.indicators.expandWindow(key, result.fromMs, Number.POSITIVE_INFINITY);
          facade.resetPriceScale();
          viewportModeRef.current = "live";
          facade.scrollToRealTime();
          chartSnapshotLoaded = true;
          flushPendingIndicatorHydration();
          uiLog.debug("chart snapshot loaded", {
            symbol: result.symbol,
            timeframe: result.timeframe,
            bars: result.bars?.length ?? 0,
            elapsedMs: Math.round((performance.now() - startedAt) * 10) / 10,
          });
        }
	  } catch {
		// A disconnected request is settled by WsClient; reconnect triggers the normal snapshot path.
	  } finally {
		chartSnapshotPending = false;
	  }
    };

    const clampRight = (range: LogicalRange | null) => {
      // Left pan remains open through blank history until oldest loaded candle
      // reaches viewport's right boundary. Past that point range contains no
      // anchors and LWC may auto-correct bar spacing (visible as zoom-in).
      // Clamp to `to = 0`, preserving logical width and therefore zoom.
      if (range && range.to < -0.5) {
        const width = range.to - range.from;
        timeScale.setVisibleLogicalRange({ from: (-0.5 - width) as Logical, to: -0.5 as Logical });
        scheduleRefreshSelection();
        return;
      }
      const visibleBars = range ? range.to - range.from : RIGHT_OFFSET_BARS;
      const target = clampRightScroll(timeScale.scrollPosition(), visibleBars);
      if (target !== null) timeScale.scrollToPosition(target, false);
      scheduleRefreshSelection();
    };
    timeScale.subscribeVisibleLogicalRangeChange(clampRight);
    const getVisibleLogicalRange = (timeScale as { getVisibleLogicalRange?: () => LogicalRange | null }).getVisibleLogicalRange;
    const initialRange = getVisibleLogicalRange ? getVisibleLogicalRange.call(timeScale) : null;
    if (initialRange) clampRight(initialRange);
    facadeRef.current = facade;
    drawingsPrimRef.current = drawings;
    setFacadePaletteRef.current = setPalette;
    const controller = new ChartController(facade, palette, { symbol, timeframe: timeframe0 },
      {
        bars: stores.bars, indicators: stores.indicators, commands,
        viewportMode: () => viewportModeRef.current,
        setViewportMode: (mode) => { viewportModeRef.current = mode; },
      });
    controller.mount();
    controllerRef.current = controller;

    // LWC's visible-range callback has no source marker, so it cannot tell a
    // user pan from a setData/update or a range restoration. Record intent only
    // from actual input gestures; the controller then preserves that intent
    // while market data changes the series.
    let pointerStart: { x: number; y: number } | null = null;
    const isDrawingUi = (target: EventTarget | null) => {
      const element = target as { closest?: (selector: string) => unknown } | null;
      return typeof element?.closest === "function" && Boolean(element.closest("[data-drawing-ui]"));
    };
    const onViewportPointerDown = (event: PointerEvent) => {
      if ((event.button === 0 || event.pointerType === "touch") && !isDrawingUi(event.target)) {
        pointerStart = { x: event.clientX, y: event.clientY };
      }
    };
    const onViewportPointerMove = (event: PointerEvent) => {
      if (!pointerStart || isDrawingUi(event.target)) return;
      if (event.buttons === 0 && event.pointerType !== "touch") return;
      if (Math.hypot(event.clientX - pointerStart.x, event.clientY - pointerStart.y) < 2) return;
      pointerStart = null;
      controller.noteUserViewportInteraction();
    };
    const onViewportPointerUp = () => { pointerStart = null; };
    const onViewportWheel = (event: WheelEvent) => {
      if (!isDrawingUi(event.target)) controller.noteUserViewportInteraction();
    };
    host.addEventListener("pointerdown", onViewportPointerDown);
    host.addEventListener("pointermove", onViewportPointerMove);
    host.addEventListener("pointerup", onViewportPointerUp);
    host.addEventListener("pointercancel", onViewportPointerUp);
    host.addEventListener("wheel", onViewportWheel);

    // Restore persisted indicator instances (colors + params) saved with the workspace.
    for (const inst of instances) controller.addIndicator(inst);
    // Restore any pane collapsed at persist time — the pane exists synchronously
    // once its series are added above, so this can run right after the loop.
    for (const inst of instances) {
      if (inst.collapsed) controller.setPaneCollapsed(INDICATOR_CATALOG[inst.type].slots[0].paneIndex, true);
    }
    if (chartType !== "candle") controller.setChartType(chartType);
    controller.setShowSessions(chartSettings.sessionShading);
    controller.setGrid(chartSettings.grid);
    controller.setVolumeVisible(chartSettings.volume);
    controller.setWatermark(chartSettings.watermark);
    drawings.setHideAll(hideAll);

    let currentSymbol = linkGroups.symbolFor(groupRef.current) ?? symbol;
    let lastOpenedSymbol = "";
    let lastOpenedTimeframe = "";

    const interaction = new DrawingInteraction(
      host,
      facade,
      drawings,
      stores.drawings,
      {
        symbol: () => currentSymbol,
        bars: () => stores.bars.series(currentSymbol, tfRef.current),
        timeframeMs: () => timeframeToMs(tfRef.current as Timeframe),
      },
      {
        onToolChange: (t) => setActiveTool(t),
        // Ref-indirected (not `refreshSelection` captured directly): this callback
        // is bound once, in the [config.id]-only mount effect, while refreshSelection
        // is redefined every render (it closes over chartSymbol/palette). Reading
        // through refreshSelRef — the same indirection the paint loop and clampRight
        // already use — always calls the current render's version instead of the
        // stale one captured at mount time.
        onSelectionChange: () => refreshSelRef.current?.(),
        styleForKind: (k) => stores.drawingToolStyles.styleFor(k),
      },
    );
    interactionRef.current = interaction;

    const backfillFills = (sym: string) => {
      controller.setFills(aggregateFillMarkers(stores.fills.forSymbolFills(sym), tfRef.current as Timeframe));
      void commands.sendQuery("QueryFills", { symbol: sym, fromMs: 0, toMs: Date.now() })
        .then((payload) => { stores.fills.ingest((payload as Parameters<typeof stores.fills.ingest>[0]) ?? []); })
        .catch(() => { /* reconnect triggers the next chart refresh */ });
    };
    let pendingFirstPaint: { symbol: string; timeframe: string; startedAt: number; sequence: number } | null = null;
    let lastFirstPaintSequence = 0;
    let initialized = false;
    let firstPaintLogFrame: number | null = null;
    const applySymbol = () => {
      // Pinning detaches from the bus without changing the symbol currently on
      // screen. PanelFrame persists that snapshot for the next workspace load.
      const linkedSymbol = linkGroups.symbolFor(groupRef.current);
      if (linkedSymbol) ownSymbolRef.current = linkedSymbol;
      const nextSymbol = linkedSymbol ?? ownSymbolRef.current;
      const nextTimeframe = tfRef.current;
      const symbolChanged = nextSymbol !== lastOpenedSymbol;
      const timeframeChanged = nextTimeframe !== lastOpenedTimeframe;
      // Link-group notifications can repeat the displayed symbol. Reloading it
      // would reset live indicator series such as VWAP.
      if (initialized && !symbolChanged && !timeframeChanged) return;
      currentSymbol = nextSymbol;
      lastOpenedSymbol = nextSymbol;
      lastOpenedTimeframe = nextTimeframe;
      const timing = linkGroups.focusTimingFor(groupRef.current, currentSymbol);
      if (timing && timing.sequence > lastFirstPaintSequence) {
        pendingFirstPaint = { ...timing, timeframe: tfRef.current };
        lastFirstPaintSequence = timing.sequence;
      }
      viewportGeneration++;
      chartGenerationRef.current++;
      pendingIndicatorHydrationRef.current.clear();
      indicatorReloadPending = initialized;
      initialized = true;
      chartSnapshotLoaded = false;
	  chartSnapshotPending = false;
      controller.setSymbol(currentSymbol);
      backfillFills(currentSymbol);
      stores.drawings.ensureLoaded(currentSymbol);
      interactionRef.current?.onSymbolChanged();
      setChartSymbol(currentSymbol);
      forceRepaintRef.current = true;
	  if (!symbolChanged && timeframeChanged) {
        void querySnapshot(); // timeframe-only switch: engine mirror is ready
      }
    };
    applySymbolRef.current = applySymbol;
    applySymbol();
    const offLink = linkGroups.subscribe(applySymbol);
    // SysEvent.seq is only monotonic within its producer (the hub, health
    // poller, venue seeder, etc.), not across the combined sys.events stream.
    // Remember the concrete events currently in HealthStore instead of using a
    // global high-water mark: an unrelated seq=20 must not hide a later hub
    // history-ready seq=1 and strand a freshly re-specified indicator.
    const eventKey = (event: { seq: number; ts: string; kind: string; detail: string; level?: string }) =>
      `${event.kind}\u0000${event.seq}\u0000${event.ts}\u0000${event.detail}\u0000${event.level ?? ""}`;
    let seenEventKeys = new Set(stores.health.getSnapshot().events.map(eventKey));
    const offHistoryReady = stores.health.subscribe(() => {
      const events = stores.health.getSnapshot().events;
      const nextSeen = new Set<string>();
      for (const event of events) {
        const key = eventKey(event);
        nextSeen.add(key);
        if (seenEventKeys.has(key)) continue;
        if (event.kind === "indicator-ready") {
          const pending = pendingIndicatorHydrationRef.current.get(event.detail);
          if (!pending || pending.generation !== chartGenerationRef.current) continue;
          if (pending.readyDebt === 0) continue;
          pending.readyDebt--;
          if (pending.readyDebt === 0) void queryIndicatorHydration(pending.instanceId);
          continue;
        }
        if (event.detail !== currentSymbol || event.kind !== "chart-ready") continue;
        if (indicatorReloadPending) {
          for (const indicatorKey of indicatorKeys()) stores.indicators.reset(indicatorKey);
          indicatorReloadPending = false;
        }
		void querySnapshot();
      }
      // HealthStore retains only its bounded event window. Mirroring that window
      // here keeps this dedup state bounded too.
      seenEventKeys = nextSeen;
    });

    const updateLegend = () => {
      const bars = controller.displayBars();
      legendRef.current?.update(computeLegendView(bars, stores.indicators, instancesRef.current, paletteRef.current, crosshairLogicalRef.current));
    };
    // subscribeCrosshairMove has the same unthrottled-input-rate shape as
    // subscribeVisibleLogicalRangeChange above: it fires on every native
    // pointermove, and updateLegend does an O(indicators x bars) scan
    // (computeLegendView -> valueAt) plus per-indicator DOM writes. Recording
    // the crosshair position is cheap and stays synchronous; the expensive
    // recompute is deferred to once per frame, same rAF-batching pattern.
    let legendFrame: number | null = null;
    const offCrosshair = facade.subscribeCrosshairMove((logical) => {
      crosshairLogicalRef.current = logical;
      if (legendFrame !== null) return;
      legendFrame = requestAnimationFrame(() => { legendFrame = null; updateLegend(); });
    });

    // Each chart panel tracks its own last-seen revision per store, rather than
    // consuming a shared boolean flag — BarStore/IndicatorStore are shared across
    // every chart panel in a workspace (see App.tsx's single makeStores() call), so
    // a shared consume-and-reset flag would let only the first-visited panel each
    // frame ever see the change, starving every other panel including its own
    // initial backfill. Sentinel -1 guarantees the first check after mount is
    // always "dirty" (so a panel mounting after data already exists still picks
    // it up on its very first scheduled frame, not just on the next new message).
    let lastBarsRev = -1;
    let lastIndicatorsRev = -1;
    let lastFillsRev = -1;
    let lastDrawingsRev = -1;
    let lastWallBucket = -1;
    let lastBoundaryTraceKey = "";
    // Dragging a pane separator (e.g. resizing the MACD sub-pane by hand) changes
    // pane heights inside LWC directly — it bumps no store revision and moves no
    // crosshair, so none of the revision checks above ever see it. Without this
    // fingerprint, isDirty() stays false, paint() never runs, and the legend +
    // pane-control button cluster (positioned from paneOffsets/rightAxisWidth,
    // recomputed only in paint() below) stay pinned at their pre-drag coordinates.
    // Comparing a cheap join of heights + axis width catches any pane-geometry
    // change — drag, collapse, close — self-correcting every frame during a
    // continuous drag.
    let lastPaneSig = "";
    const off = scheduler.register({
      id: `chart:${config.id}`,
      isDirty: () => {
        const barsRev = stores.bars.getRev(currentSymbol, tfRef.current);
        // Recomputed fresh every call (not cached via the `[instances]` effect
        // below): instancesRef.current is kept synchronously authoritative by
        // setInstancesNow specifically to avoid a same-tick double-mutation bug
        // (see its own comment), but the `[instances]` effect that would refresh
        // a cached key list only runs later, on React's next commit — a window
        // where a cache could read stale. isDirty() already runs unconditionally
        // every rAF tick for every registered surface (Scheduler.paintFrame), and
        // describeIndicator() per instance is a small, non-O(bars) allocation
        // (a handful of slots across typically 0-7 instances), so recomputing
        // here instead of caching sidesteps the staleness question entirely at
        // no meaningful cost.
        let indicatorsRev = 0;
        for (const inst of instancesRef.current) {
          for (const d of describeIndicator(inst, paletteRef.current)) indicatorsRev += stores.indicators.getRev(d.key);
        }
        const fillsRev = stores.fills.getRev();
        const drawingsRev = stores.drawings.getRev();
        const paneSig = `${facade.paneHeights().join(",")}|${facade.priceScaleWidth()}`;
        const wallBucket = usesBoundaryManagedFollow(tfRef.current)
          ? Math.floor(stores.marketClock.nowMs() / timeframeToMs(tfRef.current as Timeframe))
          : -1;
        const changed = barsRev !== lastBarsRev || indicatorsRev !== lastIndicatorsRev || fillsRev !== lastFillsRev || drawingsRev !== lastDrawingsRev
          || paneSig !== lastPaneSig || wallBucket !== lastWallBucket || forceRepaintRef.current;
        lastBarsRev = barsRev;
        lastIndicatorsRev = indicatorsRev;
        lastFillsRev = fillsRev;
        lastDrawingsRev = drawingsRev;
        lastPaneSig = paneSig;
        lastWallBucket = wallBucket;
        forceRepaintRef.current = false;
        return changed;
      },
      paint: () => {
        const browserNowMs = Date.now();
        const nowMs = stores.marketClock.nowMs();
        if (chartSnapshotLoaded) controller.sync(nowMs);
        const paintedBars = chartSnapshotLoaded ? stores.bars.series(currentSymbol, tfRef.current) : [];
        if (pendingFirstPaint && pendingFirstPaint.symbol === currentSymbol && pendingFirstPaint.timeframe === tfRef.current && paintedBars.length > 0) {
          const timing = pendingFirstPaint;
          pendingFirstPaint = null;
          firstPaintLogFrame = requestAnimationFrame(() => {
            firstPaintLogFrame = null;
            uiLog.debug("chart first bars painted", {
              symbol: timing.symbol,
              timeframe: timing.timeframe,
              bars: paintedBars.length,
              elapsedMs: Math.round((performance.now() - timing.startedAt) * 10) / 10,
            });
          });
        }
        // Diagnostic-only (Task 6): how many times this sync() actually paid the
        // Intl.DateTimeFormat cost (buildDaySegment) versus hitting the cached day
        // segment. Guard here, not just inside recordScan: skipping the call avoids
        // building the `chart:${config.id}` template-literal id on every hot-path
        // paint while disabled (mirrors TapePanel.tsx's identical recordScan guard).
        if (perf.enabled) perf.recordScan(`chart:${config.id}`, controller.lastSyncDaySegmentBuilds());
        if (chartSnapshotLoaded && usesBoundaryManagedFollow(tfRef.current)) {
          const timeframe = tfRef.current as Timeframe;
          const marketBoundaryMs = bucketStartMs(nowMs, timeframe);
          const traceKey = `${currentSymbol}|${timeframe}|${marketBoundaryMs}`;
          if (traceKey !== lastBoundaryTraceKey) {
            const rawTail = stores.bars.series(currentSymbol, timeframe).at(-1);
            const rawTailMs = rawTail ? Date.parse(rawTail.bucketStart) : NaN;
            const browserBoundaryMs = bucketStartMs(browserNowMs, timeframe);
            const clock = stores.marketClock.snapshot();
            uiLog.debug("chart market clock boundary", {
              symbol: currentSymbol,
              timeframe,
              browserNowMs,
              marketNowMs: Math.round(nowMs),
              browserBoundaryMs,
              marketBoundaryMs,
              rawTailBucketMs: Number.isFinite(rawTailMs) ? rawTailMs : null,
              browserLeadMs: Number.isFinite(rawTailMs) ? rawTailMs - browserBoundaryMs : null,
              marketLeadMs: Number.isFinite(rawTailMs) ? rawTailMs - marketBoundaryMs : null,
              synchronized: clock.synchronized,
              effectiveOffsetMs: clock.offsetMs,
              browserToEngineOffsetMs: clock.browserToEngineOffsetMs,
              marketOffsetMs: clock.marketOffsetMs,
              engineTimeMs: clock.engineTimeMs,
              browserRttMs: clock.browserRttMs,
              marketSampleAgeMs: clock.marketSampleAgeMs,
              marketSampleRttMs: clock.marketSampleRttMs,
              marketClockStale: clock.stale,
            });
            lastBoundaryTraceKey = traceKey;
          }
        }
        controller.setFills(aggregateFillMarkers(stores.fills.forSymbolFills(currentSymbol), tfRef.current as Timeframe));
        drawings.setDrawings(stores.drawings.forSymbol(currentSymbol));
        drawings.setBars(controller.barsMs(), timeframeToMs(tfRef.current as Timeframe));
        drawings.requestUpdate();
        updateLegend();
        refreshSelRef.current?.();
        const heights = facade.paneHeights();
        const offs = heights.map((_, i) => heights.slice(0, i).reduce((a, b) => a + b, 0));
        setPaneOffsets((prev) => (prev.length === offs.length && prev.every((v, i) => v === offs[i]) ? prev : offs));
        const axisW = facade.priceScaleWidth();
        setRightAxisWidth((prev) => (prev === axisW ? prev : axisW));
        // Anchor on the newest eligible raw exchange bar, not only an
        // in-progress tail. Quiet/delayed buckets retain the last confirmed
        // same-day price while the active ET session remains open.
        const rawBars = chartSnapshotLoaded ? stores.bars.series(currentSymbol, tfRef.current) : [];
        const candidate = latestEligibleCountdownBar(rawBars, tfRef.current as Timeframe, nowMs);
        const y = candidate ? facade.priceToCoordinate(candidate.c) : null;
        const next = candidate && y != null && Number.isFinite(y) ? { y, up: candidate.c >= candidate.o, price: candidate.c } : null;
        setLastPriceTag((prev) =>
          prev === next || (prev && next && prev.y === next.y && prev.up === next.up && prev.price === next.price) ? prev : next);
      },
    });

    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      controller.resize(Math.floor(r.width), Math.floor(r.height));
    });
    ro.observe(host);

    return () => {
      disposed = true;
      viewportGeneration++;
      chartGenerationRef.current++;
      pendingIndicatorHydrationRef.current.clear();
      off(); offLink(); offHistoryReady(); offCrosshair(); ro.disconnect();
      host.removeEventListener("pointerdown", onViewportPointerDown);
      host.removeEventListener("pointermove", onViewportPointerMove);
      host.removeEventListener("pointerup", onViewportPointerUp);
      host.removeEventListener("pointercancel", onViewportPointerUp);
      host.removeEventListener("wheel", onViewportWheel);
      timeScale.unsubscribeVisibleLogicalRangeChange(clampRight);
      // Cancel any rAF-batched legend/selection recompute still pending from
      // the schedulers above so it doesn't fire after this chart unmounts.
      if (legendFrame !== null) cancelAnimationFrame(legendFrame);
      if (selectionFrame !== null) cancelAnimationFrame(selectionFrame);
      if (firstPaintLogFrame !== null) cancelAnimationFrame(firstPaintLogFrame);
      interaction.dispose(); controller.dispose(); controllerRef.current = null; interactionRef.current = null;
    };
    // Intentionally [config.id] only: symbol/timeframe/indicator/palette changes are
    // handled imperatively via the controller (see the effects/callbacks below) — the
    // chart must never remount on those changes (the canvas keeps its context).
  }, [config.id]);

  // Group re-assignment (Bug: switching this chart's color group updated the
  // header but left the candles on the previous group's symbol). The mount
  // effect above only reacts to a group's *focused symbol* changing
  // (linkGroups.subscribe(applySymbol)); re-picking THIS panel's group calls
  // neither that subscription nor anything else the mount effect depends on. The
  // guard is a no-op on mount (groupRef seeds to the same initial `group`) and
  // only fires applySymbol again when the group actually changes afterward.
  useEffect(() => {
    if (groupRef.current !== group) {
      groupRef.current = group;
      applySymbolRef.current?.();
    }
  }, [group]);

  useEffect(() => {
    if (!symbolProp || symbolProp === ownSymbolRef.current) return;
    ownSymbolRef.current = symbolProp;
    if (groupRef.current === null) applySymbolRef.current?.();
  }, [symbolProp]);

  // Theme switch: re-apply palette to chart, series and the custom primitives.
  useEffect(() => {
    controllerRef.current?.setPalette(palette);
    setFacadePaletteRef.current?.(palette);
  }, [palette]);

  useEffect(() => { instancesRef.current = instances; }, [instances]);
  useEffect(() => { paletteRef.current = palette; }, [palette]);

  // ---- config mutations: drive the controller imperatively, then persist ----
  // Patch-only: AppShell merges patches into the stored settings, so each write
  // carries just the keys it changes. Re-asserting the other keys from render
  // state here would clobber newer values with stale closures (this `config` is
  // frozen at panel creation — dockview never re-invokes the factory).
  const persist = (patch: Record<string, unknown>) => onConfigChange(patch);
  const queueIndicatorHydration = (inst: IndicatorInstance) => {
    const previous = pendingIndicatorHydrationRef.current.get(inst.instanceId);
    const readyDebt = previous?.generation === chartGenerationRef.current ? previous.readyDebt + 1 : 1;
    pendingIndicatorHydrationRef.current.set(inst.instanceId, {
      instanceId: inst.instanceId,
      seriesKeys: describeIndicator(inst, paletteRef.current).map((series) => series.key),
      readyDebt,
      generation: chartGenerationRef.current,
      querying: false,
      retryUsed: false,
    });
  };

  const changeTimeframe = (tf: string) => {
    // Imperative chart reset/query runs before React commits state. Keep hot-path
    // ref authoritative now; otherwise Weekly switch resubscribes engine to W
    // while the snapshot query still requests stale D (and vice versa).
    tfRef.current = tf;
    setTf(tf); controllerRef.current?.setTimeframe(tf); applySymbolRef.current?.(); forceRepaintRef.current = true; persist({ timeframe: tf });
  };
  // Every mutation goes through instancesRef (updated synchronously here, not
  // just by the post-render effect below): two mutations in the same tick would
  // otherwise both read this render's stale `instances` closure and the second
  // would silently drop the first (observed live as an indicator whose series
  // the controller drew but whose legend row/persisted entry vanished).
  const setInstancesNow = (next: IndicatorInstance[]) => {
    instancesRef.current = next;
    setInstances(next);
    persist({ indicators: next });
  };
  const addIndicator = (type: IndicatorType) => {
    const inst: IndicatorInstance = { instanceId: `${config.id}:${type}-${idSeq.current++}`, type, params: withDefaultParams(type) };
    queueIndicatorHydration(inst);
    controllerRef.current?.addIndicator(inst);
    setInstancesNow([...instancesRef.current, inst]);
  };
  const updateIndicator = (inst: IndicatorInstance) => {
    const previous = instancesRef.current.find((i) => i.instanceId === inst.instanceId);
    const paramsChanged = previous !== undefined
      && JSON.stringify(withDefaultParams(previous.type, previous.params))
        !== JSON.stringify(withDefaultParams(inst.type, inst.params));
    if (paramsChanged) {
      for (const series of describeIndicator(inst, paletteRef.current)) stores.indicators.reset(series.key);
      queueIndicatorHydration(inst);
    }
    controllerRef.current?.updateIndicator(inst);
    setInstancesNow(instancesRef.current.map((i) => (i.instanceId === inst.instanceId ? inst : i)));
  };
  const removeIndicator = (id: string) => {
    pendingIndicatorHydrationRef.current.delete(id);
    controllerRef.current?.removeIndicator(id);
    setInstancesNow(instancesRef.current.filter((i) => i.instanceId !== id));
  };
  const toggleIndicatorHidden = (id: string) => {
    const inst = instancesRef.current.find((i) => i.instanceId === id);
    if (inst) updateIndicator({ ...inst, hidden: !inst.hidden });
  };
  const instancesInPane = (paneIndex: number) =>
    instancesRef.current.filter((i) => INDICATOR_CATALOG[i.type].slots[0].paneIndex === paneIndex);
  // LWC settles a pane-height change (setStretchFactor, or a series/pane removal)
  // on its OWN next animation frame — one tick after ours. A single forced repaint
  // here reads facade.paneHeights() before that lands, so paneOffsets/rightAxisWidth
  // (and the legend + pane-control cluster positioned from them) capture the
  // pre-change height and stay stuck there until some unrelated event happens to
  // repaint again. Force a second repaint one frame later so they settle onto the
  // post-resize values instead of drifting permanently out of alignment with the
  // pane they label (verified live: without this, collapse/expand/close visibly
  // desyncs the button cluster from the pane boundary).
  const forceRepaintNextFrame = () => {
    forceRepaintRef.current = true;
    requestAnimationFrame(() => { forceRepaintRef.current = true; });
  };
  // Pane-header "X": removes every indicator living in that sub-pane (in practice
  // just MACD — it's the only indicator type with a sub-pane of its own). The LWC
  // pane itself auto-removes once its last series is gone (removeIndicator below).
  const closePane = (paneIndex: number) => {
    for (const inst of instancesInPane(paneIndex)) removeIndicator(inst.instanceId);
    forceRepaintNextFrame();
  };
  const togglePaneCollapsed = (paneIndex: number) => {
    const inPane = instancesInPane(paneIndex);
    if (inPane.length === 0) return;
    const next = !(inPane[0].collapsed ?? false);
    controllerRef.current?.setPaneCollapsed(paneIndex, next);
    setInstancesNow(instancesRef.current.map((i) =>
      INDICATOR_CATALOG[i.type].slots[0].paneIndex === paneIndex ? { ...i, collapsed: next } : i));
    forceRepaintNextFrame();
  };
  const toggleHideAll = () => {
    const next = !hideAll; setHideAll(next); drawingsPrimRef.current?.setHideAll(next);
    drawingsPrimRef.current?.requestUpdate(); persist({ hideAllDrawings: next });
  };
  const toggleDrawingTools = () => {
    const next = !drawingToolsVisible;
    if (!next) {
      setActiveTool("select");
      interactionRef.current?.setTool("select");
    }
    setDrawingToolsVisible(next);
    persist({ drawingToolsVisible: next });
  };
  const applyChartSettings = (s: ChartSettings) => {
    setChartSettings(s);
    const c = controllerRef.current;
    c?.setShowSessions(s.sessionShading); c?.setGrid(s.grid); c?.setVolumeVisible(s.volume); c?.setWatermark(s.watermark);
    forceRepaintRef.current = true; persist({ chartSettings: s });
  };
  const onScreenshot = () => {
    const canvas = facadeRef.current?.takeScreenshot();
    if (!canvas) return;
    try {
      const a = document.createElement("a");
      a.href = canvas.toDataURL("image/png");
      a.download = `${chartSymbol.replace(/^US\./, "")}-${timeframe}.png`;
      a.click();
    } catch { /* jsdom canvas has no 2d backend; the screenshot API was still exercised */ }
  };
  const patchSelected = (patch: Partial<Pick<Drawing, "color" | "width" | "lineStyle">>) => {
    const id = interactionRef.current?.selectedId(); if (!id) return;
    const d = stores.drawings.forSymbol(chartSymbol).find((x) => x.id === id); if (!d) return;
    stores.drawings.upsert({ ...d, ...patch, updatedMs: Date.now() });
    // Remember this edit as the tool's new default so the NEXT drawing of the
    // same kind starts with it, instead of only affecting the drawing just edited.
    stores.drawingToolStyles.remember(d.kind, patch);
    forceRepaintRef.current = true;
  };
  const cloneSelected = () => {
    const id = interactionRef.current?.selectedId(); if (!id) return;
    const d = stores.drawings.forSymbol(chartSymbol).find((x) => x.id === id); if (!d) return;
    const now = Date.now();
    stores.drawings.upsert({ ...d, id: crypto.randomUUID(), anchors: d.anchors.map((a) => ({ ...a })), createdMs: now, updatedMs: now });
    forceRepaintRef.current = true;
  };
  const refreshSelection = () => {
    const di = interactionRef.current;
    const id = di?.selectedId() ?? null;
    if (!id) { setSelection((prev) => (prev ? null : prev)); return; }
    const rect = di!.selectedRect();
    const d = stores.drawings.forSymbol(chartSymbol).find((x) => x.id === id);
    if (!rect || !d) { setSelection((prev) => (prev ? null : prev)); return; }
    const color = d.color ?? palette.text;
    const width = d.width ?? 1;
    const lineStyle = (d.lineStyle ?? "solid") as LineStyleName;
    // Compare style fields too, not just id/rect — moving a drawing's anchors isn't
    // the only way it changes: editing color/width/lineStyle via the floating
    // toolbar leaves rect untouched, and returning the stale `prev` object here
    // (a plain `setSelection(prev)` no-op) left the toolbar's own controls frozen
    // on the pre-edit values even though the canvas repainted correctly.
    setSelection((prev) => (prev && prev.id === id && prev.rect.x === rect.x && prev.rect.y === rect.y && prev.rect.w === rect.w && prev.rect.h === rect.h
      && prev.color === color && prev.width === width && prev.lineStyle === lineStyle
      ? prev
      : { id, rect, color, width, lineStyle }));
  };
  useEffect(() => { refreshSelRef.current = refreshSelection; });

  const onContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    const r = hostRef.current!.getBoundingClientRect();
    // x/y are host-local (for hit-testing + coordinateToPrice below); clientX/clientY
    // are viewport-relative, which is what the menu's `position: fixed` needs — mixing
    // these up puts the menu on the wrong chart when charts aren't at the viewport origin.
    const x = e.clientX - r.left, y = e.clientY - r.top;
    const drawingId = interactionRef.current?.hitTestAt({ x, y }) ?? null;
    if (drawingId) { interactionRef.current?.select(drawingId); refreshSelection(); }
    setMenu({ x, y, clientX: e.clientX, clientY: e.clientY, drawingId });
  };
  const buildMenuItems = (m: { x: number; y: number; drawingId: string | null }): MenuEntry[] => {
    const items: MenuEntry[] = [];
    if (m.drawingId) {
      items.push({ label: "Clone", onClick: cloneSelected });
      items.push({ label: "Delete", danger: true, onClick: () => interactionRef.current?.deleteSelection() });
      items.push("separator");
    }
    items.push({ label: "Reset chart view", onClick: () => { controllerRef.current?.resetZoom(); forceRepaintRef.current = true; } });
    items.push({ label: "Jump to live", onClick: () => { controllerRef.current?.jumpToLive(); forceRepaintRef.current = true; } });
    const price = facadeRef.current?.coordinateToPrice(m.y) ?? null;
    if (price !== null) items.push({ label: `Copy price ${price.toFixed(2)}`, onClick: () => void navigator.clipboard?.writeText(price.toFixed(2)) });
    items.push("separator");
    items.push({ label: "Remove all drawings", danger: true, onClick: () => stores.drawings.clearSymbol(chartSymbol) });
    items.push({ label: hideAll ? "Show all drawings" : "Hide all drawings", onClick: toggleHideAll });
    items.push("separator");
    const inWatch = stores.watchlist.has(chartSymbol);
    items.push(
      inWatch
        ? { label: `Remove ${bareSymbol(chartSymbol)} from watchlist`, danger: true,
            onClick: () => void commands.sendCommand("WatchlistRemove", { symbol: chartSymbol }) }
        : { label: `Add ${bareSymbol(chartSymbol)} to watchlist`,
            onClick: () => void commands.sendCommand("WatchlistAdd", { symbol: chartSymbol }) },
    );
    items.push("separator");
    items.push({ label: "Settings…", onClick: () => setChartSettingsOpen(true) });
    return items;
  };

  // Drives BarCloseTimer's merged price+countdown badge — and, in lockstep, LWC's
  // own last-value tag: whenever this is true the tag is suppressed (see the effect
  // below) so the badge's price row is the only thing drawn at that coordinate,
  // rather than doubling up behind it.
  const showBarCloseTimer = chartSettings.barCloseTimer && isIntradayTimeframe(timeframe as Timeframe) && !!lastPriceTag && rightAxisWidth > 0;
  useEffect(() => {
    controllerRef.current?.setLastValueVisible(!showBarCloseTimer);
  }, [showBarCloseTimer]);

  // Timeframe/indicators/screenshot/settings render in PanelFrame's ledger-header
  // slot (portalled) instead of a second strip in this body — see headerSlot.ts.
  // headerSlot === undefined means no PanelFrame above (e.g. a body-level test
  // rendering ChartPanel directly): fall back to rendering inline so the controls
  // still exist in this panel's own subtree. headerSlot === null means the slot
  // provider is mounted but its DOM node hasn't attached yet — render nothing for
  // that one tick rather than flash the controls inline first.
  const headerControls = <ChartHeaderControls palette={appPalette} timeframe={timeframe}
    onTimeframe={changeTimeframe} onAddIndicator={addIndicator}
    onScreenshot={onScreenshot} onOpenSettings={() => setChartSettingsOpen(true)}
    drawingToolsVisible={drawingToolsVisible} onToggleDrawingTools={toggleDrawingTools} />;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", background: chrome.bg }}>
      {headerSlot === undefined ? headerControls : headerSlot ? createPortal(headerControls, headerSlot) : null}
      <div ref={hostRef} data-testid="chart-host" tabIndex={0} style={{ flex: 1, minHeight: 0, position: "relative" }}
        onContextMenu={onContextMenu}>
        {drawingToolsVisible && <TVDrawingRail chrome={chrome} activeTool={activeTool} hideAll={hideAll} symbol={chartSymbol}
          onSelectTool={(t) => { setActiveTool(t); interactionRef.current?.setTool(t); }}
          onToggleHideAll={toggleHideAll}
          hasSelection={() => interactionRef.current?.hasSelection() ?? false}
          onDeleteSelection={() => interactionRef.current?.deleteSelection()}
          onClearAll={() => stores.drawings.clearSymbol(chartSymbol)}
          initialPos={drawingRailPos} onPosChange={(p) => { setDrawingRailPos(p); persist({ drawingRailPos: p }); }} />}
        <TVLegend chrome={chrome} symbol={chartSymbol} timeframe={timeframe} instances={instances} paneOffsets={paneOffsets}
          rightAxisWidth={rightAxisWidth}
          onToggleHidden={toggleIndicatorHidden} onEditIndicator={setSettingsInstanceId} onRemoveIndicator={removeIndicator}
          onClosePane={closePane} onToggleCollapsePane={togglePaneCollapsed}
          legendRef={legendRef} />
        {showBarCloseTimer && lastPriceTag && (
          <BarCloseTimer now={stores.marketClock.nowMs} chrome={chrome} timeframe={timeframe} price={formatPrice(lastPriceTag.price, 2)} lastPriceY={lastPriceTag.y}
            rightAxisWidth={rightAxisWidth} paneBottom={paneOffsets[1] ?? height} up={lastPriceTag.up} />
        )}
        {selection && (
          <TVFloatingToolbar chrome={chrome} rect={selection.rect} color={selection.color} width={selection.width} lineStyle={selection.lineStyle}
            onColor={(c) => patchSelected({ color: c })} onWidth={(w) => patchSelected({ width: w })} onLineStyle={(s) => patchSelected({ lineStyle: s })}
            onClone={cloneSelected} onDelete={() => interactionRef.current?.deleteSelection()} />
        )}
        {menu && <TVContextMenu chrome={chrome} x={menu.clientX} y={menu.clientY} items={buildMenuItems(menu)} onClose={() => setMenu(null)} />}
      </div>
      {settingsInstanceId && (() => {
        const inst = instances.find((i) => i.instanceId === settingsInstanceId);
        if (!inst) return null;
        return (
          <IndicatorSettingsDialog chrome={chrome} instance={inst} resolved={describeIndicator(inst, palette)}
            onClose={() => setSettingsInstanceId(null)} onApply={updateIndicator} />
        );
      })()}
      {chartSettingsOpen && <ChartSettingsDialog chrome={chrome} settings={chartSettings} onClose={() => setChartSettingsOpen(false)} onApply={applyChartSettings} />}
    </div>
  );
}
