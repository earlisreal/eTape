import { useEffect, useRef, useState } from "react";
import { createChart, createTextWatermark, CandlestickSeries, BarSeries, HistogramSeries, LineSeries, AreaSeries, type IChartApi, type ISeriesApi, type Time, type Logical, type Coordinate } from "lightweight-charts";
import type { PanelProps } from "./registry";
import { ChartController } from "../../render/chart/ChartController";
import { clampRightScroll } from "../../render/chart/chartTheme";
import type { ChartApiFacade, LwcSeries } from "../../render/chart/ChartApiFacade";
import { DiamondFillPrimitive } from "../../render/chart/diamondPrimitive";
import { SessionShadingPrimitive } from "../../render/chart/sessionPrimitive";
import { INDICATOR_CATALOG, withDefaultParams, type IndicatorInstance, type IndicatorType } from "../../render/chart/indicatorSeries";
import { DrawingsPrimitive } from "../../render/chart/drawings/primitive";
import { DrawingInteraction, type Tool } from "../../render/chart/drawings/interaction";
import { DrawingRail } from "./DrawingRail";
import { timeframeToMs } from "../../render/chart/drawings/geometry";
import type { Timeframe } from "../../render/chart/barBucket";
import type { Palette } from "../../render/palette";
import { ChartControls } from "./ChartControls";
import { useTheme } from "../ThemeProvider";

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
    scrollToRealTime: () => chart.timeScale().scrollToRealTime(),
    resetTimeScale: () => chart.timeScale().resetTimeScale(),
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
    remove: () => chart.remove(),
  };
  return { facade, setPalette: (p) => { session.setPalette(p); diamonds.setPalette(p); drawings.setPalette(p); }, drawings };
}

export function ChartPanel({ config, stores, scheduler, width, height, linkGroups, commands, onConfigChange, group: groupProp }: PanelProps): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const controllerRef = useRef<ChartController | null>(null);
  const setFacadePaletteRef = useRef<((p: Palette) => void) | null>(null);
  const idSeq = useRef(0);
  const { palette } = useTheme();
  const symbol = (config.settings.symbol as string) ?? "US.AAPL";
  const timeframe0 = (config.settings.timeframe as string) ?? "1m";
  // config.group is frozen (dockview captures this panel's factory once, at
  // creation, and never re-invokes it with a fresh config on a later group
  // re-pick — see PanelFrame's `group` prop comment). PanelFrame threads its own
  // live `group` state through as a prop; fall back to config.group so tests
  // that construct PanelProps directly (no `group` prop) keep working.
  const group = groupProp ?? config.group;

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
  const magnetRef = useRef(true);
  const tfRef = useRef<string>(timeframe0);
  // Timeframe/symbol switches clear the controller's series synchronously but
  // bump no store revision, so the scheduler's revision-based isDirty() would
  // otherwise never repaint them until an unrelated bar delta happens to
  // arrive. This flag forces exactly one repaint on the next scheduled frame.
  const forceRepaintRef = useRef(false);
  const [activeTool, setActiveTool] = useState<Tool>("select");
  const [magnet, setMagnet] = useState(true);
  const [chartSymbol, setChartSymbol] = useState(symbol);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  useEffect(() => { tfRef.current = timeframe; }, [timeframe]);

  // The mount effect below is [config.id]-only (the chart/canvas must never
  // remount on a symbol/group/timeframe change — see that effect's closing
  // comment), so it captures `group` at mount time. groupRef lets the reactive
  // effect further down (which DOES see live `group` changes) tell the
  // already-mounted closure "the group was reassigned, re-resolve the symbol" —
  // applySymbolRef is that closure's own applySymbol, captured once it's created.
  const groupRef = useRef(group);
  const applySymbolRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const chart = createChart(host, { width, height });
    // Right-edge pan cap: LWC has no native "capped but non-zero" right-edge option
    // (fixRightEdge hardcodes the margin to 0 — see chartTheme's rightOffset comment),
    // so bound it here. scrollPosition() is the distance in bars from the right edge
    // to the latest bar; snapping it back (without changing bar spacing) preserves
    // zoom. The re-fired event after scrollToPosition is a no-op second pass since
    // scrollPosition() then equals the cap.
    const timeScale = chart.timeScale();
    const clampRight = () => {
      const target = clampRightScroll(timeScale.scrollPosition());
      if (target !== null) timeScale.scrollToPosition(target, false);
    };
    timeScale.subscribeVisibleLogicalRangeChange(clampRight);
    const { facade, setPalette, drawings } = makeFacade(chart, palette);
    setFacadePaletteRef.current = setPalette;
    const controller = new ChartController(facade, palette, { symbol, timeframe: timeframe0 },
      { bars: stores.bars, indicators: stores.indicators, commands });
    controller.mount();
    controllerRef.current = controller;

    // Restore persisted indicator instances (colors + params) saved with the workspace.
    for (const inst of instances) controller.addIndicator(inst);

    let currentSymbol = linkGroups.symbolFor(groupRef.current) ?? symbol;

    const interaction = new DrawingInteraction(
      host,
      facade,
      drawings,
      stores.drawings,
      {
        symbol: () => currentSymbol,
        bars: () => stores.bars.series(currentSymbol, tfRef.current),
        timeframeMs: () => timeframeToMs(tfRef.current as Timeframe),
        magnet: () => magnetRef.current,
      },
      { onToolChange: (t) => setActiveTool(t) },
    );
    interactionRef.current = interaction;

    const backfillFills = (sym: string) => {
      controller.setFills(stores.fills.forSymbol(sym));
      void commands.sendQuery("QueryFills", { symbol: sym, fromMs: 0, toMs: Date.now() })
        .then((payload) => { stores.fills.ingest((payload as Parameters<typeof stores.fills.ingest>[0]) ?? []); });
    };
    const applySymbol = () => {
      currentSymbol = linkGroups.symbolFor(groupRef.current) ?? symbol;
      controller.setSymbol(currentSymbol);
      backfillFills(currentSymbol);
      stores.drawings.ensureLoaded(currentSymbol);
      interactionRef.current?.onSymbolChanged();
      setChartSymbol(currentSymbol);
      forceRepaintRef.current = true;
    };
    applySymbolRef.current = applySymbol;
    applySymbol();
    const offLink = linkGroups.subscribe(applySymbol);

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
    const off = scheduler.register({
      id: `chart:${config.id}`,
      isDirty: () => {
        const barsRev = stores.bars.getRev();
        const indicatorsRev = stores.indicators.getRev();
        const fillsRev = stores.fills.getRev();
        const drawingsRev = stores.drawings.getRev();
        const changed = barsRev !== lastBarsRev || indicatorsRev !== lastIndicatorsRev || fillsRev !== lastFillsRev || drawingsRev !== lastDrawingsRev
          || forceRepaintRef.current;
        lastBarsRev = barsRev;
        lastIndicatorsRev = indicatorsRev;
        lastFillsRev = fillsRev;
        lastDrawingsRev = drawingsRev;
        forceRepaintRef.current = false;
        return changed;
      },
      paint: () => {
        controller.sync();
        controller.setFills(stores.fills.forSymbol(currentSymbol));
        drawings.setDrawings(stores.drawings.forSymbol(currentSymbol));
        drawings.setBars(
          stores.bars.series(currentSymbol, tfRef.current).map((b) => Date.parse(b.bucketStart)),
          timeframeToMs(tfRef.current as Timeframe),
        );
        drawings.requestUpdate();
      },
    });

    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      controller.resize(Math.floor(r.width), Math.floor(r.height));
    });
    ro.observe(host);

    return () => {
      off(); offLink(); ro.disconnect();
      timeScale.unsubscribeVisibleLogicalRangeChange(clampRight);
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

  // Theme switch: re-apply palette to chart, series and the custom primitives.
  useEffect(() => {
    controllerRef.current?.setPalette(palette);
    setFacadePaletteRef.current?.(palette);
  }, [palette]);

  // ---- config mutations: drive the controller imperatively, then persist ----
  const persist = (patch: Record<string, unknown>) => onConfigChange({ ...config.settings, timeframe, indicators: instances, ...patch });

  const changeTimeframe = (tf: string) => {
    setTf(tf);
    controllerRef.current?.setTimeframe(tf);
    forceRepaintRef.current = true;
    persist({ timeframe: tf });
  };
  const addIndicator = (type: IndicatorType) => {
    const inst: IndicatorInstance = { instanceId: `${config.id}:${type}-${idSeq.current++}`, type, params: withDefaultParams(type) };
    const next = [...instances, inst];
    setInstances(next); controllerRef.current?.addIndicator(inst); persist({ indicators: next });
  };
  const updateIndicator = (inst: IndicatorInstance) => {
    const next = instances.map((i) => (i.instanceId === inst.instanceId ? inst : i));
    setInstances(next); controllerRef.current?.updateIndicator(inst); persist({ indicators: next });
  };
  const removeIndicator = (id: string) => {
    const next = instances.filter((i) => i.instanceId !== id);
    setInstances(next); controllerRef.current?.removeIndicator(id); persist({ indicators: next });
  };

  const menuRow: React.CSSProperties = { padding: "5px 10px", borderRadius: 4, cursor: "pointer", fontSize: 11.5, whiteSpace: "nowrap" };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <ChartControls timeframe={timeframe} instances={instances} palette={palette}
        onTimeframe={changeTimeframe} onAdd={addIndicator} onUpdate={updateIndicator} onRemove={removeIndicator} />
      <div ref={hostRef} data-testid="chart-host" style={{ flex: 1, minHeight: 0, position: "relative" }}
        onContextMenu={(e) => {
          e.preventDefault();
          const rect = hostRef.current!.getBoundingClientRect();
          setMenu({ x: e.clientX - rect.left, y: e.clientY - rect.top });
        }}>
        <DrawingRail
          activeTool={activeTool}
          magnet={magnet}
          symbol={chartSymbol}
          onSelectTool={(t) => { setActiveTool(t); interactionRef.current?.setTool(t); }}
          onToggleMagnet={() => { magnetRef.current = !magnetRef.current; setMagnet(magnetRef.current); }}
          hasSelection={() => interactionRef.current?.hasSelection() ?? false}
          onDeleteSelection={() => interactionRef.current?.deleteSelection()}
          onClearAll={() => stores.drawings.clearSymbol(chartSymbol)}
        />
        {menu && (
          <div className="popover" style={{ left: menu.x, top: menu.y, padding: 4 }} onMouseLeave={() => setMenu(null)}>
            <div role="button" style={menuRow} onClick={() => { stores.drawings.clearSymbol(chartSymbol); setMenu(null); }}>
              Clear all drawings
            </div>
            <div role="button" style={menuRow} onClick={() => { controllerRef.current?.resetZoom(); setMenu(null); }}>
              Reset zoom
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
