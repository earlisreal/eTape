import { expect, test, type WebSocketRoute } from "@playwright/test";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";
import type { Bar, ClientMessage, QueryChartWindowArgs, ServerMessage, SysEvent } from "../src/wire/contract";

test("VWAP and EMA 9 catch up and resume streaming after a socket reconnect", async ({ page }) => {
  const workspace = "e2e-live-indicators";
  const symbol = "US.NVDA";
  const timeframe = "1m";
  const start = Date.parse("2026-09-30T14:30:00Z");
  const indicators = [
    { instanceId: "indicator-chart:VWAP-0", type: "VWAP", params: {} },
    { instanceId: "indicator-chart:EMA-1", type: "EMA", params: { period: 9 } },
  ];
  const values = [[100, 105], [101, 107], [102, 109]];
  const bar = (index: number): Bar => ({ symbol, timeframe, bucketStart: new Date(start + index * 60_000).toISOString(),
    o: 105, h: 110, l: 104, c: 106 + index, v: 100, inProgress: true });
  let connection = 0;
  let seq = 0;
  let lastBar = 0;
  let streamBar: ((index: number) => void) | undefined;
  let browserSocket: WebSocketRoute | undefined;
  const send = (socket: WebSocketRoute, message: ServerMessage) => socket.send(JSON.stringify(message));
  await page.clock.setFixedTime(new Date(start + 130_000));
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    const subscribed = new Set<string>();
    const isReconnect = ++connection > 1;
    browserSocket = socket;
    const event = (kind: string, detail: string) => {
      const payload: SysEvent = { seq: ++seq, ts: new Date(start).toISOString(), kind, detail };
      send(socket, { kind: "delta", topic: "sys.events", payload });
    };
    streamBar = (index) => {
      lastBar = index;
      const payload = bar(index);
      send(socket, { kind: "delta", topic: "md.bars", key: `${symbol}:${timeframe}`, payload });
      indicators.forEach(({ instanceId }, slot) => {
        if (!subscribed.has(instanceId)) return;
        send(socket, { kind: "delta", topic: "md.indicator", key: instanceId,
          payload: { timeMs: Date.parse(payload.bucketStart), value: values[index][slot] } });
      });
    };
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
        send(socket, { kind: "ack", corrId: message.corrId, status: "accepted", value: {
          name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
          panels: [{ id: "indicator-chart", panelId: "chart", group: null, settings: { symbol, timeframe, indicators } }],
        } });
      } else if (message.kind === "command" && message.name === "EnsureSymbol" && (message.args as { symbol: string }).symbol === symbol) {
        send(socket, { kind: "ack", corrId: message.corrId, status: "accepted" });
        if (isReconnect) {
          lastBar = 1;
          send(socket, { kind: "delta", topic: "md.bars", key: `${symbol}:${timeframe}`, payload: bar(lastBar) });
        }
        event("chart-ready", symbol);
      } else if (message.kind === "command" && message.name === "SubscribeIndicator") {
        const { instanceId } = message.args as { instanceId: string };
        subscribed.add(instanceId);
        send(socket, { kind: "ack", corrId: message.corrId, status: "accepted" });
        event("indicator-ready", instanceId);
      } else if (message.kind === "query" && message.name === "QueryChartWindow") {
        const args = message.args as QueryChartWindowArgs;
        const fromMs = args.tailBars > 0 ? start : args.fromMs;
        const toMs = args.tailBars > 0 ? start + lastBar * 60_000 + 1 : args.toMs;
        const indices = Array.from({ length: lastBar + 1 }, (_, i) => i)
          .filter((i) => start + i * 60_000 >= fromMs && start + i * 60_000 < toMs);
        const series = (args.indicatorSeriesKeys ?? []).map((seriesKey) => ({ seriesKey,
          points: subscribed.has(seriesKey) ? indices.map((i) => ({ timeMs: start + i * 60_000,
            value: values[i][indicators.findIndex((inst) => inst.instanceId === seriesKey)] })) : [],
        }));
        send(socket, { kind: "result", corrId: message.corrId, payload: { symbol, timeframe, fromMs, toMs,
          bars: args.skipBars ? [] : indices.map(bar), indicators: series, historyRevision: connection } });
      } else server.send(raw);
    });
    server.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ServerMessage;
      if ((message.kind === "delta" || message.kind === "snapshot") && (message.topic === "md.bars" || message.topic === "md.indicator")) return;
      if ((message.kind === "snapshot" || message.kind === "delta") && message.topic === "sys.session") {
        send(socket, { ...message, payload: { ...message.payload as object, mode: "live" } });
      } else socket.send(raw);
    });
  });

  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
  const onboarding = page.getByRole("button", { name: "I'll do it later", exact: true });
  if (await onboarding.isVisible()) await onboarding.click();
  await page.mouse.move(0, 0);
  const vwap = page.getByTestId(`legend-ind-${indicators[0].instanceId}-0`);
  const ema = page.getByTestId(`legend-ind-${indicators[1].instanceId}-0`);
  await expect(vwap).toHaveText("100.00");
  await expect(ema).toHaveText("105.00");

  await browserSocket!.close({ code: 1000, reason: "test connection interrupted" });
  await expect(page.getByTestId("legend-c")).toHaveText("107.00");
  await expect(vwap).toHaveText("101.00");
  await expect(ema).toHaveText("107.00");

  streamBar!(2);
  await expect(page.getByTestId("legend-c")).toHaveText("108.00");
  await expect(vwap).toHaveText("102.00");
  await expect(ema).toHaveText("109.00");
});
