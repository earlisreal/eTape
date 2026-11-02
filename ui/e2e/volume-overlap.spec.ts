import { test, expect, type Page, type Locator } from "@playwright/test";
import type { ClientMessage, ServerMessage, QueryChartWindowArgs } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";

// Count fully opaque price pixels below a fractional height of the price pane.
// Translucent volume uses different blended pixels, so it cannot satisfy this.
async function lowerPricePixels(host: Locator, from = 0.8): Promise<number> {
  return host.locator("canvas").first().evaluate((canvas: HTMLCanvasElement, from: number) => {
    const { width, height } = canvas;
    const pixels = canvas.getContext("2d")!.getImageData(0, 0, width, height).data;
    let count = 0;
    for (let y = Math.ceil(height * from); y < height - 4; y++) {
      for (let x = Math.ceil(width * 0.1); x < width - 4; x++) {
        const i = (y * width + x) * 4;
        if ((pixels[i] === 8 && pixels[i + 1] === 153 && pixels[i + 2] === 129)
          || (pixels[i] === 242 && pixels[i + 1] === 54 && pixels[i + 2] === 69)) count++;
      }
    }
    return count;
  }, from);
}

test("global overlap, chart overrides and saved inheritance paint the real chart", async ({ context, page }) => {
  test.setTimeout(90_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  const config = new Map<string, unknown>();
  const now = Date.parse("2026-10-09T14:30:05Z");
  const workspace = (name: string) => ({ name, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
    panels: [{ id: `${name}-chart`, panelId: "chart", group: null,
      settings: { symbol: "US.TEST", timeframe: "1m", chartType: "candle" } }],
  });
  config.set("workspace.overlap-one", workspace("overlap-one"));
  config.set("workspace.overlap-two", workspace("overlap-two"));
  config.set("chartPreferences", { volumeOverlap: false });
  config.set("theme", "light");
  const errors: string[] = [];
  const prepare = async (target: Page) => {
    target.on("pageerror", (error) => errors.push(error.message));
    await target.clock.setFixedTime(now);
    await target.routeWebSocket("**/ws", (socket) => {
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
        if (message.kind === "query" && message.name === "QueryChartWindow") {
          const { symbol, timeframe } = message.args as QueryChartWindowArgs;
          const bucket = Math.floor(now / 60_000) * 60_000;
          socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload: {
            symbol, timeframe, fromMs: bucket - 59 * 60_000, toMs: bucket + 60_000, indicators: [], historyRevision: 1,
            bars: Array.from({ length: 60 }, (_, i) => {
              const close = 20 - i / 6;
              return { symbol, timeframe, bucketStart: new Date(bucket - (59 - i) * 60_000).toISOString(),
                o: close + 0.5, h: close + 0.7, l: close - 0.2, c: close, v: 1000 + i * 100, inProgress: i === 59 };
            }),
          } }));
        } else server.send(raw);
      });
      server.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ServerMessage;
        if ((message.kind === "snapshot" || message.kind === "delta") && message.topic === "md.bars") return;
        if (message.kind === "snapshot" && message.topic === "sys.session") {
          socket.send(JSON.stringify({ ...message, payload: { ...message.payload as object, mode: "live" } }));
        } else socket.send(raw);
      });
    });
  };
  await prepare(page);
  const second = await context.newPage();
  await prepare(second);
  const open = async (target: Page, name: string) => {
    await target.goto(`/?workspace=${name}`);
    await expect(target.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15000 });
    const later = target.getByRole("button", { name: "I'll do it later", exact: true });
    if (await later.isVisible()) await later.click();
    const watchlist = target.getByRole("tab", { name: "Watchlist", exact: true });
    if (await watchlist.count()) await watchlist.getByRole("button", { name: "Close tab" }).click();
    await expect(target.getByTestId("chart-host").first()).toBeVisible();
    await target.mouse.move(0, 0);
    await expect(target.getByTestId("legend-vol")).toContainText(/6\.9[kK]/);
  };
  await open(page, "overlap-one");
  await open(second, "overlap-two");
  const host = page.getByTestId("chart-host").first();
  const other = second.getByTestId("chart-host").first();
  await expect.poll(() => lowerPricePixels(host)).toBe(0);
  await host.screenshot({ path: test.info().outputPath("overlap-off-light.png") });
  const global = async (on: boolean) => {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByLabel("Overlap volume", { exact: true }).setChecked(on);
    await page.getByRole("button", { name: "Close settings", exact: true }).click();
  };
  const override = async (value: string) => {
    await second.getByRole("button", { name: "chart settings", exact: true }).first().click();
    await second.getByLabel("Overlap volume", { exact: true }).selectOption(value);
    await second.getByRole("button", { name: "Ok", exact: true }).click();
  };
  await global(true);
  await expect.poll(() => lowerPricePixels(host)).toBeGreaterThan(10);
  await expect.poll(() => lowerPricePixels(host, 0.95)).toBe(0);
  await expect.poll(() => lowerPricePixels(other)).toBeGreaterThan(10);
  await host.screenshot({ path: test.info().outputPath("overlap-on-light.png") });
  await override("off");
  await expect.poll(() => lowerPricePixels(other)).toBe(0);
  await global(false);
  await global(true);
  await expect.poll(() => lowerPricePixels(other)).toBe(0);
  await override("default");
  await expect.poll(() => lowerPricePixels(other)).toBeGreaterThan(10);
  type SavedWorkspace = { panels: { settings: Record<string, unknown> }[] };
  await expect.poll(() => (config.get("workspace.overlap-two") as SavedWorkspace).panels[0].settings.chartSettings).not.toHaveProperty("volumeOverlap");
  await open(second, "overlap-two");
  await expect.poll(() => lowerPricePixels(other)).toBeGreaterThan(10);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByLabel("Dark", { exact: true }).check();
  await page.getByRole("button", { name: "Close settings", exact: true }).click();
  await expect(second.locator("html")).toHaveAttribute("data-theme", "dark");
  await other.screenshot({ path: test.info().outputPath("overlap-on-dark.png") });
  await second.setViewportSize({ width: 460, height: 460 });
  await expect.poll(() => lowerPricePixels(other)).toBeGreaterThan(0);
  await expect.poll(() => lowerPricePixels(other, 0.95)).toBe(0);
  await other.screenshot({ path: test.info().outputPath("overlap-padding-small.png") });
  await second.setViewportSize({ width: 1280, height: 720 });
  for (const style of ["bar", "line", "area"]) {
    const current = config.get("workspace.overlap-two") as SavedWorkspace;
    current.panels[0].settings.chartType = style;
    await open(second, "overlap-two");
    await expect.poll(() => lowerPricePixels(other)).toBeGreaterThan(0);
    await expect.poll(() => lowerPricePixels(other, 0.95)).toBe(0);
    await other.screenshot({ path: test.info().outputPath(`overlap-${style}-dark.png`) });
  }
  expect(errors).toEqual([]);
});
