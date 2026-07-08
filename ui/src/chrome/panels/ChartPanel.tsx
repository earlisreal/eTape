import { useEffect, useRef, useState } from "react";
import { createChart, CandlestickSeries, HistogramSeries, LineSeries, type IChartApi, type ISeriesApi, type Time, type Logical, type Coordinate } from "lightweight-charts";
import type { PanelProps } from "./registry";
import { ChartController } from "../../render/chart/ChartController";
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
  let candle: ISeriesApi<"Candlestick"> | null = null;
  const session = new SessionShadingPrimitive(palette);
  const diamonds = new DiamondFillPrimitive(palette);
  const drawings = new DrawingsPrimitive(palette);

  const facade: ChartApiFacade = {
    addSeries: (kind, options, paneIndex) => {
      const s = kind === "candle" ? chart.addSeries(CandlestickSeries, options as object, paneIndex)
        : kind === "line" ? chart.addSeries(LineSeries, options as object, paneIndex)
        : chart.addSeries(HistogramSeries, options as object, paneIndex);
      if (kind === "candle") {
        candle = s as ISeriesApi<"Candlestick">;
        candle.attachPrimitive(diamonds);
        candle.attachPrimitive(drawings);
        // Pane primitive on the main pane (index 0) for session shading:
        chart.panes()[0]?.attachPrimitive?.(session);
      }
      return s as unknown as LwcSeries;
    },
    removeSeries: (s) => chart.removeSeries(s as unknown as ISeriesApi<"Line">),
    setPriceScaleMargins: (id, margins) => chart.priceScale(id).applyOptions({ scaleMargins: margins }),
    setSessionBands: (bands) => session.setBands(bands),
    setFillMarkers: (m) => diamonds.setMarkers(m),
    timeToCoordinate: (ms) => chart.timeScale().timeToCoordinate((Math.floor(ms / 1000)) as unknown as Time),
    priceToCoordinate: (price) => candle?.priceToCoordinate(price) ?? null,
    logicalToCoordinate: (logical) => chart.timeScale().logicalToCoordinate(logical as Logical),
    coordinateToLogical: (x) => chart.timeScale().coordinateToLogical(x as Coordinate),
    coordinateToPrice: (y) => candle?.coordinateToPrice(y as Coordinate) ?? null,
    setPanZoomEnabled: (on) => chart.applyOptions({ handleScroll: on, handleScale: on }),
    scrollToRealTime: () => chart.timeScale().scrollToRealTime(),
    resize: (w, h) => chart.resize(w, h),
    applyOptions: (o) => chart.applyOptions(o as object),
    remove: () => chart.remove(),
  };
  return { facade, setPalette: (p) => { session.setPalette(p); diamonds.setPalette(p); drawings.setPalette(p); }, drawings };
}

export function ChartPanel({ config, stores, scheduler, width, height, linkGroups, commands, onConfigChange }: PanelProps): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const controllerRef = useRef<ChartController | null>(null);
  const setFacadePaletteRef = useRef<((p: Palette) => void) | null>(null);
  const idSeq = useRef(0);
  const { palette } = useTheme();
  const symbol = (config.settings.symbol as string) ?? "US.AAPL";
  const timeframe0 = (config.settings.timeframe as string) ?? "1m";

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
  useEffect(() => { tfRef.current = timeframe; }, [timeframe]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const chart = createChart(host, { width, height });
    const { facade, setPalette, drawings } = makeFacade(chart, palette);
    setFacadePaletteRef.current = setPalette;
    const controller = new ChartController(facade, palette, { symbol, timeframe: timeframe0 },
      { bars: stores.bars, indicators: stores.indicators, commands });
    controller.mount();
    controllerRef.current = controller;

    // Restore persisted indicator instances (colors + params) saved with the workspace.
    for (const inst of instances) controller.addIndicator(inst);

    let currentSymbol = linkGroups.symbolFor(config.group) ?? symbol;

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
      currentSymbol = linkGroups.symbolFor(config.group) ?? symbol;
      controller.setSymbol(currentSymbol);
      backfillFills(currentSymbol);
      stores.drawings.ensureLoaded(currentSymbol);
      interactionRef.current?.onSymbolChanged();
      setChartSymbol(currentSymbol);
      forceRepaintRef.current = true;
    };
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

    return () => { off(); offLink(); ro.disconnect(); interaction.dispose(); controller.dispose(); controllerRef.current = null; interactionRef.current = null; };
    // Intentionally [config.id] only: symbol/timeframe/indicator/palette changes are
    // handled imperatively via the controller (see the effects/callbacks below) — the
    // chart must never remount on those changes (the canvas keeps its context).
  }, [config.id]);

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

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <ChartControls timeframe={timeframe} instances={instances} palette={palette}
        onTimeframe={changeTimeframe} onAdd={addIndicator} onUpdate={updateIndicator} onRemove={removeIndicator} />
      <div ref={hostRef} style={{ flex: 1, minHeight: 0, position: "relative" }}>
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
      </div>
    </div>
  );
}
