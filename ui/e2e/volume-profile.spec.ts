import { test, expect } from "@playwright/test";
import type { ClientMessage, QueryChartWindowArgs, QueryChartWindowResult, ServerMessage } from "../src/wire/contract";
import type { QueryVolumeProfileArgs, QueryVolumeProfileResult } from "../src/gen/wsmsg";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";
import { timeframeToMs } from "../src/render/chart/drawings/geometry";
import type { Timeframe } from "../src/render/chart/barBucket";

test("captured profile settings, passive chart gestures and unsupported modes", async ({ page }) => {
  const workspace = "e2e-volume-profile";
  const now = Date.parse("2026-10-09T14:30:05Z");
  let queries = 0;
  let mode = "live";
  const subscriptions: string[] = [];
  await page.clock.setFixedTime(now);
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind === "command" && message.name === "SubscribeIndicator") subscriptions.push(JSON.stringify(message.args));
      if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: {
          name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
          panels: [{ id: "profile-chart", panelId: "chart", group: null, settings: { symbol: "US.TEST", timeframe: "1m", indicators: [{ instanceId: "profile-chart:VOLUME_PROFILE", type: "VOLUME_PROFILE", params: { rows: 100, valueArea: 70 }, placement: "left" }] } }],
        } }));
      } else if (message.kind === "query" && message.name === "QueryChartWindow") {
        const { symbol, timeframe } = message.args as QueryChartWindowArgs;
        const span = timeframeToMs(timeframe as Timeframe);
        const bucket = Math.floor(now / span) * span;
        const payload: QueryChartWindowResult = { symbol, timeframe, fromMs: bucket - 40 * span, toMs: bucket + span, indicators: [], historyRevision: 1,
          bars: Array.from({ length: 40 }, (_, i) => ({ symbol, timeframe, bucketStart: new Date(bucket - (39 - i) * span).toISOString(), o: 10, h: 14, l: 9, c: 12, v: 75, inProgress: i === 39 })) };
        socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload }));
      } else if (message.kind === "query" && message.name === "QueryVolumeProfile") {
        queries++;
        const selection = message.args as QueryVolumeProfileArgs;
        const payload: QueryVolumeProfileResult = { selection, status: "ready", source: "captured", partial: true, reasons: ["coverage_unproven"], capturedVolume: 75, asOfMs: now, firstPrintMs: selection.fromMs, lastPrintMs: now - 1,
          rows: [{ lower: 10, upper: 11, volume: 5 }, { lower: 11, upper: 12, volume: 25 }, { lower: 12, upper: 13, volume: 35 }, { lower: 13, upper: 14, volume: 10 }], poc: 12.5, val: 11, vah: 13 };
        socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload }));
      } else server.send(raw);
    });
    server.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ServerMessage;
      if ((message.kind === "snapshot" || message.kind === "delta") && message.topic === "md.bars") return;
      if (message.kind === "snapshot" && message.topic === "sys.session") socket.send(JSON.stringify({ ...message, payload: { ...message.payload as object, mode } }));
      else socket.send(raw);
    });
  });
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15000 });
  const onboarding = page.getByRole("button", { name: "I'll do it later", exact: true });
  if (await onboarding.isVisible()) await onboarding.click();
  const host = page.getByTestId("chart-host").first();
  const frame = host.locator("xpath=ancestor::*[@role='region'][1]");
  const row = frame.getByTestId("legend-row-profile-chart:VOLUME_PROFILE");
  const value = frame.getByTestId("legend-profile-profile-chart:VOLUME_PROFILE");
  await expect(value).toContainText("Partial · Captured 75 · POC 12.5000");
  await row.hover();
  await frame.getByRole("button", { name: "settings profile-chart:VOLUME_PROFILE", exact: true }).click();
  await page.getByLabel("Placement", { exact: true }).selectOption("right");
  await page.getByRole("button", { name: "Ok", exact: true }).click();
  await expect(value).toContainText("POC 12.5000");
  await host.screenshot({ path: test.info().outputPath("volume-profile-right.png") });
  const box = (await host.boundingBox())!;
  const before = queries;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, -200);
  await expect.poll(() => queries).toBeGreaterThan(before);
  await frame.getByRole("button", { name: "timeframe D", exact: true }).click();
  await expect(value).toContainText("unavailable");
  await expect(value).toHaveAttribute("title", "Intraday charts only");
  expect(subscriptions.some((s) => s.includes("VOLUME_PROFILE"))).toBe(false);
  mode = "demo";
  await page.reload();
  await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
  await expect(value).toContainText("unavailable");
  await expect(value).toHaveAttribute("title", "Unavailable in demo mode");
});
