import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createRoot } from "react-dom/client";
import { createChart, CandlestickSeries } from "lightweight-charts";
import { ChartOrderMarkers } from "../../src/chrome/panels/tv/ChartOrderMarkers";
import { ChartStopLimitEntry } from "../../src/chrome/panels/tv/ChartStopLimitEntry";
import { makeStores } from "../../src/data/registry";
import { LinkGroups } from "../../src/chrome/linkGroups";
import "../../src/global.css";

// This harness never creates a socket, broker adapter, or engine connection.
const stores = makeStores();
const linkGroups = new LinkGroups({ post() {}, onMessage() { return () => {}; }, close() {} }, () => {});
linkGroups.focusVenue("green", "sim");
stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: {
  masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "sim", broker: "sim", env: "paper", connected: true, reconcilePending: false, note: "",
    lastReconcileMs: null, heldStopLimitAcknowledged: true,
    gate: { maxOrderValue: 10000, maxPositionValue: 10000, maxPositionShares: 100, maxOpenOrders: 10 } }],
} });
stores.exec.apply({ kind: "snapshot", topic: "exec.account", key: "sim", payload: {
  venue: "sim", equity: 10000, buyingPower: 10000, availableCash: 10000, sodEquity: 10000,
  realized: 0, dayPnl: 0, leverage: 1, tsMs: 1, cycleStartMs: 0, cycleRealized: 0,
} });
const config = { activeVenue: "sim", templates: [{ kind: "place", id: "stop", label: "Stop", side: "BUY", type: "STOP_LIMIT",
  tif: "DAY", session: "EXTENDED", priceSource: "Last", priceOffset: 0, limitCushion: 0, limitCushionUnit: "$",
  chartBinding: "Shift", sizing: { mode: "Shares", shares: 1 } }] };
let serial = 0;
const sendCommand = async (name, args) => {
  if (args.venue !== "sim") throw new Error("Only simulated orders are accepted");
  if (name === "SubmitOrder") {
    const id = `sim-${++serial}`;
    window.repro.lastSubmitted = args;
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: {
      ...args, id, status: "ACCEPTED", executedQty: 0, leavesQty: args.qty, avgFillPrice: 0,
      rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, held: { phase: "WAITING", deadlineMs: Date.now() + 3600000 },
    } });
    return { kind: "ack", corrId: id, status: "accepted", orderId: id };
  }
  if (name === "CancelOrder") {
    const order = stores.exec.getSnapshot().orders.get(args.orderId);
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { ...order, status: "CANCELED" } });
    return { kind: "ack", corrId: args.orderId, status: "accepted" };
  }
  throw new Error(`Unsupported simulated command: ${name}`);
};
const sendQuery = async () => ({ route: "ENGINE_HELD", effectiveSession: "EXTENDED", deadlineMs: Date.now() + 3600000,
  phase: "PRE", hasTrustedEligiblePrint: true, lastEligiblePrice: 4.4 });

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
    const observer = new ResizeObserver(([e]) => chart.resize(Math.floor(e.contentRect.width), Math.floor(e.contentRect.height)));
    observer.observe(host);
    window.repro = { lastSubmitted: null, ready: true, measure(cursorY = 280) {
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
        previewStopText: host.querySelector("[data-entry-chip]")?.textContent,
        submittedStop: window.repro.lastSubmitted?.stopPrice,
        markers: host.querySelectorAll("[data-order-group]").length,
        range: chart.timeScale().getVisibleLogicalRange(), candleY: series.priceToCoordinate(4.8) };
    } };
    requestAnimationFrame(() => { layoutRef.current(); setAxisWidth(chart.priceScale("right").width()); });
    return () => { observer.disconnect(); chart.remove(); };
  }, []);
  useEffect(() => { requestAnimationFrame(() => layoutRef.current()); });
  return <div data-testid="panel-body" style={{ position: "absolute", left: 80, top: 100, width: 760, height: 560, overflow: "hidden", display: "flex", flexDirection: "column" }}>
    <div style={{ height: 26, flexShrink: 0 }}>Sim-only AAPL chart · Shift+click</div>
    <div data-testid="chart-host" ref={hostRef} tabIndex={0} style={{ flex: 1, minHeight: 0, position: "relative" }}>
      <div style={{ position: "absolute", zIndex: 5 }}>AAPL · Vol</div>
      <ChartOrderMarkers orders={snapshot.orders.values()} venue="sim" symbol="US.AAPL" pinned={false} sendCommand={sendCommand}
        hostRef={hostRef} facadeRef={facadeRef} rightAxisWidth={axisWidth} layoutRef={layoutRef} chooserOpenRef={chooserOpenRef} />
      <ChartStopLimitEntry hostRef={hostRef} facadeRef={facadeRef} stores={stores} linkGroups={linkGroups} group="green" symbol="US.AAPL"
        config={config} configLoaded activeTool="select" chooserOpenRef={chooserOpenRef} sendCommand={sendCommand} sendQuery={sendQuery} />
    </div>
  </div>;
}
createRoot(document.getElementById("root")).render(<App />);
