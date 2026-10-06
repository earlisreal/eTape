import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import { createChart, CandlestickSeries } from "lightweight-charts";
import { installOrderCrosshair } from "../../src/render/chart/orderCrosshair";
import { ChartOrderMarkers } from "../../src/chrome/panels/tv/ChartOrderMarkers";
import { ChartConditionalOrderEntry } from "../../src/chrome/panels/tv/ChartConditionalOrderEntry";
import { OrderConfigProvider, useOrderConfig } from "../../src/chrome/exec/useOrderConfig";
import { ThemeProvider, useTheme } from "../../src/chrome/ThemeProvider";
import { initiateChartRiskEntry } from "../../src/chrome/exec/actionTemplate";
import { getTvChrome } from "../../src/render/chart/tvTheme";
import { ToastProvider } from "../../src/chrome/Toast";
import { PanelFrame } from "../../src/chrome/PanelFrame";
import { makeStores } from "../../src/data/registry";
import { LinkGroups } from "../../src/chrome/linkGroups";
import { Scheduler } from "../../src/render/Scheduler";
import { browserRaf } from "../../src/render/surface";
import "../../src/global.css";

// This harness never creates a socket, broker adapter, or engine connection.
const stores = makeStores();
const linkGroups = new LinkGroups({ post() {}, onMessage() { return () => {}; }, close() {} }, () => {});
linkGroups.focusVenue("green", "sim");
stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: {
  masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "sim", broker: "sim", env: "paper", connected: true, reconcilePending: false, note: "",
    lastReconcileMs: null, heldStopLimitAcknowledged: true, positionDataReady: true,
    gate: { maxOrderValue: 10000, maxPositionValue: 10000, maxPositionShares: 100, maxOpenOrders: 10 } }],
} });
stores.exec.apply({ kind: "snapshot", topic: "exec.account", key: "sim", payload: {
  venue: "sim", equity: 10000, buyingPower: 10000, availableCash: 10000, sodEquity: 10000,
  realized: 0, dayPnl: 0, leverage: 1, tsMs: Date.now(), cycleStartMs: 0, cycleRealized: 0,
} });
const riskTemplate = { kind: "risk", id: "risk", label: "Chart Risk Entry", mode: "Dollar", value: 100,
  buyCushion: { value: 0, unit: "$" }, sellCushion: { value: 0, unit: "$" } };
const config = { activeVenue: "sim", templates: [{ kind: "place", id: "stop", label: "Stop", side: "BUY", type: "STOP_LIMIT",
  tif: "DAY", session: "EXTENDED", priceSource: "Last", priceOffset: 0, limitCushion: 0.05, limitCushionUnit: "$",
  chartBinding: "Shift", sizing: { mode: "Shares", shares: 1 } }, riskTemplate] };
let serial = 0;
let nextCancelBehavior = "accepted";
let pendingCancelResolve = null;
const sendCommand = async (name, args) => {
  if (name === "GetConfig") return { kind: "ack", corrId: "config", status: "accepted", value: config };
  if (name === "SetConfig") return { kind: "ack", corrId: "config", status: "accepted" };
  if (name === "SubscribeIndicator" || name === "UnsubscribeIndicator") return { kind: "ack", corrId: "indicator", status: "accepted" };
  if (name === "SetAccountDemand") {
    if (args.venue && args.venue !== "sim") throw new Error("Only simulated account demand is accepted");
    return { kind: "ack", corrId: "demand", status: "accepted" };
  }
  if (args.venue !== "sim") throw new Error("Only simulated orders are accepted");
  if (name === "SubmitRiskEntry") {
    window.chartPanelProbe.lastRiskSubmitted = args;
    return { kind: "ack", corrId: "risk", status: "accepted", orderId: "sim-risk" };
  }
  if (name === "SubmitOrder") {
    const id = `sim-${++serial}`;
    if (window.repro) window.repro.lastSubmitted = args;
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: {
      ...args, id, status: "ACCEPTED", executedQty: 0, leavesQty: args.qty, avgFillPrice: 0,
      rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, held: { phase: "WAITING", deadlineMs: Date.now() + 3600000 },
    } });
    return { kind: "ack", corrId: id, status: "accepted", orderId: id };
  }
  if (name === "ReplaceOrder") {
    window.repro.lastReplace = args;
    const order = stores.exec.getSnapshot().orders.get(args.orderId);
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: {
      ...order, limitPrice: args.limitPrice, stopPrice: args.stopPrice, updatedMs: order.updatedMs + 1,
    } });
    return { kind: "ack", corrId: args.orderId, status: "accepted" };
  }
  if (name === "CancelOrder") {
    const behavior = nextCancelBehavior;
    nextCancelBehavior = "accepted";
    if (behavior === "pending") return new Promise(resolve => {
      pendingCancelResolve = () => {
        const order = stores.exec.getSnapshot().orders.get(args.orderId);
        stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { ...order, status: "CANCELED" } });
        pendingCancelResolve = null;
        resolve({ kind: "ack", corrId: args.orderId, status: "accepted" });
      };
    });
    if (behavior === "rejected") return { kind: "ack", corrId: args.orderId, status: "rejected", reason: "simulated rejection" };
    if (behavior === "unknown") return { kind: "ack", corrId: args.orderId, status: "accepted", ambiguous: true };
    const order = stores.exec.getSnapshot().orders.get(args.orderId);
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { ...order, status: "CANCELED" } });
    return { kind: "ack", corrId: args.orderId, status: "accepted" };
  }
  throw new Error(`Unsupported simulated command: ${name}`);
};
const chartBars = Array.from({ length: 80 }, (_, i) => {
  const bucketStart = new Date(Date.parse("2026-10-01T13:30:00.000Z") + i * 60_000).toISOString();
  const close = 5.3 - i * 0.01;
  return { symbol: "US.AAPL", timeframe: "1m", bucketStart, o: close + 0.03, h: close + 0.1,
    l: close - 0.1, c: close, v: 100 + i, inProgress: false };
});
const sendQuery = async (name, args) => {
  if (name === "QueryChartWindow") return { ...(args ?? {}), symbol: "US.AAPL", timeframe: "1m",
    fromMs: Date.parse(chartBars[0].bucketStart), toMs: Date.parse(chartBars.at(-1).bucketStart) + 60_000,
    bars: chartBars, indicators: [], historyRevision: 1 };
  if (name === "QueryFills") return [];
  return { route: "ENGINE_HELD", effectiveSession: "EXTENDED", deadlineMs: Date.now() + 3600000,
    phase: "PRE", hasTrustedEligiblePrint: true, lastEligiblePrice: 4.4, lastEligibleTsMs: Date.now() };
};

function App() {
  const hostRef = useRef(null), facadeRef = useRef(null), layoutRef = useRef(() => {}), chooserOpenRef = useRef(false);
  const snapshot = useSyncExternalStore(cb => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const [axisWidth, setAxisWidth] = useState(56);
  useEffect(() => {
    const host = hostRef.current;
    const chart = createChart(host, { width: host.clientWidth, height: host.clientHeight,
      layout: { background: { color: "#f6f7f8" }, textColor: "#111" },
      rightPriceScale: { minimumWidth: 56 }, timeScale: { timeVisible: true, secondsVisible: true } });
    const series = chart.addSeries(CandlestickSeries);
    series.setData(Array.from({ length: 80 }, (_, i) => ({ time: 1790840000 + i * 10, open: 5.3 - i * .01,
      high: 5.4 - i * .01, low: 5.2 - i * .01, close: 5.25 - i * .01 })));
    chart.timeScale().fitContent();
    facadeRef.current = {
      priceScaleWidth: () => chart.priceScale("right").width(), paneHeights: () => chart.panes().map(p => p.getHeight()),
      priceToCoordinate: p => series.priceToCoordinate(p), coordinateToPrice: y => series.coordinateToPrice(y),
    };
    const cursor = installOrderCrosshair(chart, host, facadeRef.current, () => "#787B86");
    facadeRef.current.setOrderCrosshair = cursor.set;
    facadeRef.current.setPanZoomEnabled = on => chart.applyOptions({handleScroll:on,handleScale:on});
    const observer = new ResizeObserver(([e]) => chart.resize(Math.floor(e.contentRect.width), Math.floor(e.contentRect.height)));
    observer.observe(host);
    window.repro = { lastSubmitted: null, lastReplace: null, ready: true, measure(cursorY = 280) {
      const hostBox = host.getBoundingClientRect(), panelBox = host.closest("[data-testid='panel-body']").getBoundingClientRect();
      const nativeBox = host.querySelector(".tv-lightweight-charts").getBoundingClientRect();
      const table = host.querySelector(".tv-lightweight-charts table"), axisBox = table.rows[table.rows.length - 1].getBoundingClientRect();
      const line = host.querySelector("[data-entry-line]"), ghostBox = line?.getBoundingClientRect();
      const localY = cursorY - hostBox.top, canvasY = cursorY - nativeBox.top;
      const actualStop = Math.round(series.coordinateToPrice(localY) * 100) / 100;
      const expectedStop = Math.round(series.coordinateToPrice(canvasY) * 100) / 100;
      return { hostTop: hostBox.top, hostBottom: hostBox.bottom, nativeTop: nativeBox.top, nativeBottom: nativeBox.bottom,
        nativeOffset: nativeBox.top - hostBox.top, axisTop: axisBox.top, axisBottom: axisBox.bottom,
        axisClippedPx: Math.max(0, axisBox.bottom - hostBox.bottom), actualStop, expectedStop,
        panelAxisClippedPx: Math.max(0, axisBox.bottom - panelBox.bottom),
        stopDrawnY: nativeBox.top + series.priceToCoordinate(actualStop), ghostY: ghostBox?.top,
        announcement: host.querySelector(".chart-order-announcement")?.textContent,
        previewStopText: host.querySelector("[data-testid=chart-order-entry-preview]")?.dataset.price,
        previewDetailText: host.title,
        submittedStop: window.repro.lastSubmitted?.stopPrice,
        submittedLimit: window.repro.lastSubmitted?.limitPrice,
        markers: host.querySelectorAll("[data-order-group]").length,
        range: chart.timeScale().getVisibleLogicalRange(), candleY: series.priceToCoordinate(4.8) };
    }, pointForPrice(price) { const rect=host.getBoundingClientRect(); return {x:rect.left+200,y:rect.top+series.priceToCoordinate(price)}; }, crosshairOptions() { return chart.options().crosshair; }, addOrder(id, type, price, side = "BUY") {
      stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: {
        venue: "sim", id, symbol: "US.AAPL", side, type, tif: "DAY", session: "EXTENDED", qty: 1,
        limitPrice: type === "LIMIT" ? price : price - 0.05, stopPrice: type === "STOP_LIMIT" ? price : 0,
        status: "ACCEPTED", executedQty: 0, leavesQty: 1, avgFillPrice: 0, rejectReason: "", replacesId: "",
        createdMs: 1, updatedMs: 1, ...(type === "STOP_LIMIT" ? { held: { phase: "WAITING", deadlineMs: Date.now() + 3600000 } } : {}),
      } });
    }, setNextCancelBehavior(behavior) { nextCancelBehavior = behavior; },
    resolvePendingCancel() { pendingCancelResolve?.(); },
    finishOrder(id) {
      const order = stores.exec.getSnapshot().orders.get(id);
      stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { ...order, status: "CANCELED" } });
    },
    setZoom(range) { chart.timeScale().setVisibleLogicalRange(range); },
    resizePanel(width, height) {
      const panel = host.closest("[data-testid='panel-body']");
      panel.style.width = `${width}px`; panel.style.height = `${height}px`;
    } };
    requestAnimationFrame(() => { layoutRef.current(); setAxisWidth(chart.priceScale("right").width()); });
    return () => { observer.disconnect(); cursor.dispose(); chart.remove(); };
  }, []);
  useEffect(() => { requestAnimationFrame(() => layoutRef.current()); });
  return <div data-testid="panel-body" style={{ position: "absolute", left: 80, top: 100, width: 760, height: 560, overflow: "hidden", display: "flex", flexDirection: "column" }}>
    <div style={{ height: 26, flexShrink: 0 }}>Sim-only AAPL chart · Shift+click</div>
    <div data-testid="chart-host" ref={hostRef} tabIndex={0} style={{ flex: 1, minHeight: 0, position: "relative" }}>
      <div style={{ position: "absolute", zIndex: 5 }}>AAPL · Vol</div>
      <ChartOrderMarkers chrome={getTvChrome("light")} orders={snapshot.orders.values()} venue="sim" symbol="US.AAPL" pinned={false} sendCommand={sendCommand}
        config={config} activeTool="select" hostRef={hostRef} facadeRef={facadeRef} rightAxisWidth={axisWidth} layoutRef={layoutRef} chooserOpenRef={chooserOpenRef} />
      <ChartConditionalOrderEntry chrome={getTvChrome("light")} hostRef={hostRef} facadeRef={facadeRef} stores={stores} linkGroups={linkGroups} group="green" symbol="US.AAPL"
        config={config} configLoaded activeTool="select" chooserOpenRef={chooserOpenRef} sendCommand={sendCommand} sendQuery={sendQuery} />
    </div>
  </div>;
}

function ConfigReadyProbe() {
  const { loaded } = useOrderConfig();
  const { setMode } = useTheme();
  useEffect(() => {
    window.chartPanelProbe = { isReady: () => loaded && stores.bars.series("US.AAPL", "1m").length === chartBars.length
      && !!document.querySelector("[data-testid='chart-host'] .tv-lightweight-charts"),
      errors: [], setTheme: setMode, startRisk() {
        stores.exec.apply({ kind: "snapshot", topic: "exec.account", payload: { ...stores.exec.accounts()[0], tsMs: Date.now() } });
        return initiateChartRiskEntry(riskTemplate);
      } };
    if (loaded) setTimeout(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: new Date().toISOString(), kind: "chart-ready", detail: "US.AAPL",
    } }), 0);
  }, [loaded, setMode]);
  return null;
}

function ProductionChartPanel() {
  const [scheduler] = useState(() => new Scheduler(browserRaf, (_id, error) => {
    window.chartPanelProbe?.errors.push(String(error));
  }));
  useEffect(() => { scheduler.start(); return () => scheduler.stop(); }, [scheduler]);
  const chartConfig = { id: "chart-order-layout", panelId: "chart", group: "green",
    settings: { symbol: "US.AAPL", timeframe: "1m" } };
  const demandRegistry = { ensure: async () => ({ kind: "ack", corrId: "demand", status: "accepted" }), release() {} };
  const panelApi = { isActive: true, onDidActiveChange: () => ({ dispose() {} }) };
  return <div data-testid="panel-shell" style={{ position: "absolute", left: 80, top: 100, width: 760, height: 560 }}>
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={{ sendCommand }}>
      <ConfigReadyProbe />
      <PanelFrame config={chartConfig} stores={stores} scheduler={scheduler} linkGroups={linkGroups} demandRegistry={demandRegistry}
        commands={{ sendCommand, sendQuery }} onConfigChange={() => {}} onGroupChange={() => {}} onClose={() => {}} api={panelApi} />
    </OrderConfigProvider></ToastProvider></ThemeProvider>
  </div>;
}

createRoot(document.getElementById("root")).render(location.search === "?production" ? <ProductionChartPanel /> : <App />);
