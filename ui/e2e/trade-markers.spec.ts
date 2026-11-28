import { test, expect, type Locator, type Page, type WebSocketRoute } from "@playwright/test";
import { PNG } from "pngjs";
import type { ClientMessage, Fill, QueryChartWindowArgs, ServerMessage } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";

async function markerPixels(host: Locator): Promise<number[]> {
  const { data } = PNG.sync.read(await host.screenshot());
  // Solid centers of the translucent green, pink and blue diamonds over white.
  const colors = [[183, 222, 185], [246, 165, 193], [77, 171, 245]];
  return colors.map(([r, g, b]) => {
    let count = 0;
    for (let i = 0; i < data.length; i += 4) {
      if (Math.abs(data[i] - r) <= 2 && Math.abs(data[i + 1] - g) <= 2 && Math.abs(data[i + 2] - b) <= 2) count++;
    }
    return count;
  });
}

test("trade marker visibility is saved per chart and repaints without new bars", async ({ context, page }) => {
  test.setTimeout(90_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  const now = Date.parse("2026-10-09T14:30:05Z");
  const minute = 60_000;
  const symbol = "US.MERI";
  const historical: Fill[] = (["BUY", "SELL", "SHORT", "COVER"] as const).map((side, index) => ({
    venue: "sim-paper", orderId: side, symbol, side, qty: 1, price: 20.55 + index * 0.28,
    tsMs: now - (7 + 5 * index) * minute,
  }));
  const live: Fill = { ...historical[0], orderId: "late-buy", tsMs: now - 3 * minute };
  const workspace = (name: string) => ({
    name, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
    panels: [{ id: `${name}-chart`, panelId: "chart", group: null,
      settings: { symbol, timeframe: "1m", chartType: "candle", chartSettings: { grid: false } } }],
  });
  const config = new Map<string, unknown>([
    ["theme", "light"],
    ["workspace.markers-one", workspace("markers-one")],
    ["workspace.markers-two", workspace("markers-two")],
  ]);
  const prepare = async (target: Page) => {
    const connection: { socket?: WebSocketRoute } = {};
    await target.clock.setFixedTime(now);
    await target.routeWebSocket("**/ws", (socket) => {
      connection.socket = socket;
      const server = socket.connectToServer();
      socket.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ClientMessage;
        if (message.kind === "command" && (message.name === "GetConfig" || message.name === "SetConfig")) {
          const args = message.args as { key: string; value?: unknown };
          if (config.has(args.key)) {
            if (message.name === "SetConfig") config.set(args.key, args.value);
            socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: config.get(args.key) }));
            return;
          }
        }
        if (message.kind === "query" && message.name === "QueryFills") {
          socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload: historical }));
          return;
        }
        if (message.kind === "query" && message.name === "QueryChartWindow") {
          const { timeframe } = message.args as QueryChartWindowArgs;
          const step = timeframe === "5m" ? 5 * minute : minute;
          const bucket = Math.floor(now / step) * step;
          socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload: {
            symbol, timeframe, fromMs: bucket - 59 * step, toMs: bucket + step,
            indicators: [], historyRevision: 1,
            bars: Array.from({ length: 60 }, (_, i) => ({
              symbol, timeframe, bucketStart: new Date(bucket - (59 - i) * step).toISOString(),
              o: 20, h: 22, l: 19, c: 20.1, v: 1000,
            })),
          } }));
          return;
        }
        server.send(raw);
      });
      server.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ServerMessage;
        if ((message.kind === "snapshot" || message.kind === "delta")
          && (message.topic === "md.bars" || message.topic === "exec.fills")) return;
        if (message.kind === "snapshot" && message.topic === "sys.session") {
          socket.send(JSON.stringify({ ...message, payload: { ...message.payload as object, mode: "live" } }));
          return;
        }
        socket.send(raw);
      });
    });
    return connection;
  };
  const first = await prepare(page);
  const second = await context.newPage();
  await prepare(second);
  const showChart = async (target: Page) => {
    await expect(target.getByTestId("boot-status-banner")).toHaveCount(0);
    const later = target.getByRole("button", { name: "I'll do it later", exact: true });
    if (await later.isVisible()) await later.click();
    await target.getByRole("tab", { name: "chart", exact: true }).click();
    await target.mouse.move(0, 0);
  };
  await page.goto("/?workspace=markers-one");
  await showChart(page);
  await second.goto("/?workspace=markers-two");
  await showChart(second);
  const host = page.getByTestId("chart-host").first();
  const sibling = second.getByTestId("chart-host").first();
  const visible = (counts: number[]) => counts.every((count) => count > 20);
  await expect.poll(async () => (await markerPixels(host)).map((count) => count > 20)).toEqual([true, true, true]);
  await expect.poll(async () => visible(await markerPixels(sibling))).toBe(true);
  const initial = await markerPixels(host);
  expect(initial[0]).toBeGreaterThan(initial[1] * 1.5);

  const toggle = async (show: boolean) => {
    await page.getByRole("button", { name: "chart settings", exact: true }).first().click();
    await page.getByRole("checkbox", { name: "Show trade markers" }).setChecked(show);
    await page.getByRole("button", { name: "Ok", exact: true }).click();
    await page.mouse.move(0, 0);
  };
  await toggle(false);
  await expect.poll(() => markerPixels(host)).toEqual([0, 0, 0]);
  expect(visible(await markerPixels(sibling))).toBe(true);
  expect(first.socket).toBeDefined();
  first.socket!.send(JSON.stringify({ kind: "delta", topic: "exec.fills", payload: live }));
  await expect.poll(() => markerPixels(host)).toEqual([0, 0, 0]);
  await toggle(true);
  await expect.poll(async () => (await markerPixels(host))[0]).toBeGreaterThan(initial[0] + 20);

  await toggle(false);
  const savedChart = () => (config.get("workspace.markers-one") as ReturnType<typeof workspace>).panels
    .find((panel) => panel.panelId === "chart")!.settings;
  await expect.poll(savedChart).toMatchObject({ chartSettings: { grid: false, showTradeMarkers: false } });
  // Older layouts can still choose a bar chart through their saved settings.
  savedChart().chartType = "bar";
  await page.reload();
  await showChart(page);
  await page.getByRole("button", { name: "chart settings", exact: true }).first().click();
  await expect(page.getByRole("checkbox", { name: "Show trade markers" })).not.toBeChecked();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "timeframe 5m", exact: true }).click();
  await expect.poll(() => markerPixels(host)).toEqual([0, 0, 0]);
  await toggle(true);
  await expect.poll(async () => visible(await markerPixels(host))).toBe(true);
});
