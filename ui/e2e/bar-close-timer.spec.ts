import { test, expect } from "@playwright/test";
import { PNG } from "pngjs";
import type { ClientMessage, QueryChartWindowArgs, QueryChartWindowResult, ServerMessage } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";
import { bucketStartMs, type Timeframe } from "../src/render/chart/barBucket";
import { timeframeToMs } from "../src/render/chart/drawings/geometry";

for (const layout of ["saved chart", "Trading preset"]) {
  test(`the intraday countdown paints above the axis and below the crosshair (${layout})`, async ({ page }) => {
    const workspace = `e2e-bar-close-timer-${layout.replaceAll(" ", "-")}`;
    const now = Date.parse("2026-09-30T14:31:05Z");
    await page.clock.setFixedTime(now);
    await page.routeWebSocket("**/ws", (socket) => {
      const server = socket.connectToServer();
      socket.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ClientMessage;
        if (layout === "saved chart" && message.kind === "command" && message.name === "GetConfig"
          && (message.args as { key: string }).key === `workspace.${workspace}`) {
          socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: {
            name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
            panels: [{ id: "timer-chart", panelId: "chart", group: null, settings: { symbol: "US.GLDN", timeframe: "1m" } }],
          } }));
        } else if (message.kind === "query" && message.name === "QueryChartWindow") {
          const { symbol, timeframe } = message.args as QueryChartWindowArgs;
          const span = timeframeToMs(timeframe as Timeframe);
          const bucket = bucketStartMs(now, timeframe as Timeframe);
          const payload: QueryChartWindowResult = {
            symbol, timeframe, fromMs: bucket - span, toMs: bucket + span, indicators: [], historyRevision: 1,
            bars: [0, 1].map((index) => ({ symbol, timeframe, bucketStart: new Date(bucket - span + index * span).toISOString(),
              o: 100, h: 103, l: 99, c: 101 + index, v: 100, inProgress: index === 1 })),
          };
          socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload }));
        } else server.send(raw);
      });
      server.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ServerMessage;
        if ((message.kind === "snapshot" || message.kind === "delta") && message.topic === "md.bars") return;
        socket.send(message.kind === "pong" ? JSON.stringify({ kind: "pong", t: message.t }) : raw);
      });
    });
    await page.goto(`/?workspace=${workspace}`);
    await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
    if (layout === "saved chart") {
      await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
    } else {
      await page.getByRole("button", { name: /^Trading/ }).click();
      await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
      const chart = page.getByTestId("chart-host").first();
      await chart.click();
      await page.keyboard.type("GLDN");
      await page.keyboard.press("Enter");
      await expect(chart.locator("xpath=ancestor::*[@role='region'][1]").getByTestId("panel-symbol")).toHaveText("GLDN");
      await page.waitForTimeout(800); // WorkspaceStore's 500ms save debounce.
      await page.reload(); // Exercise charts with a symbol already assigned at mount.
      await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
    }
    await page.mouse.move(0, 0);

    const host = page.getByTestId("chart-host").first();
    const frame = host.locator("xpath=ancestor::*[@role='region'][1]");
    const badge = host.getByTestId("bar-close-timer");
    const paintedColor = async () => {
      const shot = PNG.sync.read(await badge.screenshot());
      // Empty padding inside the countdown row, away from text and rounded corners.
      const offset = ((shot.height - 4) * shot.width + 4) * 4;
      return Array.from(shot.data.subarray(offset, offset + 3));
    };
    for (const [timeframe, countdown] of [
      ["1m", "0:55"], ["10s", "0:05"], ["5m", "3:55"],
      ["15m", "13:55"], ["30m", "28:55"], ["60m", "58:55"],
    ]) {
      const button = frame.getByRole("button", { name: `timeframe ${timeframe}`, exact: true });
      if (await button.isVisible()) await button.click();
      else await frame.getByRole("combobox", { name: "timeframe", exact: true }).selectOption(timeframe);
      await page.mouse.move(0, 0);
      await expect(badge.getByTestId("bar-close-timer-price")).toHaveText("102.000");
      await expect(badge.getByTestId("bar-close-timer-countdown")).toHaveText(countdown);
      const background = await badge.evaluate((element) => getComputedStyle(element).backgroundColor);
      const expected = background.match(/\d+/g)!.slice(0, 3).map(Number);
      await expect.poll(paintedColor).toEqual(expected);

      const hostBox = (await host.boundingBox())!;
      const box = (await badge.boundingBox())!;
      await page.mouse.move(hostBox.x + hostBox.width - box.width - 20, box.y + box.height - 4);
      await expect.poll(paintedColor).toEqual([19, 23, 34]); // LWC's native crosshair label.
    }
  });
}
