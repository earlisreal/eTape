import { test, expect, type Page } from "@playwright/test";

async function applyTrading(page: Page, workspace: string): Promise<string> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: /^Trading/ }).click();
  await expect(page.getByTestId("chart-host")).toHaveCount(3);
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
  const chartFrame = page.getByTestId("chart-host").first().locator("xpath=../../..");
  await chartFrame.getByTestId("panel-body").click();
  await page.keyboard.type("GLDN");
  await page.keyboard.press("Enter");
  const symbol = page.getByTestId("panel-symbol").first();
  await expect(symbol).toHaveText("GLDN");
  return (await symbol.textContent())?.trim() ?? "";
}

async function trackCrosshairMessages(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const received: unknown[] = [];
    const trackedWindow = window as unknown as {
      __crosshairReceived: unknown[];
      __crosshairInteractions: number;
      __crosshairStrokeCount: number;
    };
    trackedWindow.__crosshairReceived = received;
    trackedWindow.__crosshairInteractions = 0;
    trackedWindow.__crosshairStrokeCount = 0;
    for (const type of ["pointerdown", "pointermove", "click", "keydown", "submit"]) {
      window.addEventListener(type, () => { trackedWindow.__crosshairInteractions++; }, true);
    }
    const originalStroke = CanvasRenderingContext2D.prototype.stroke;
    CanvasRenderingContext2D.prototype.stroke = function (path?: Path2D) {
      const color = String(this.strokeStyle).replace(/\s/g, "").toLowerCase();
      const firstChart = document.querySelector('[data-testid="chart-host"]');
      if ((color === "rgb(120,123,134)" || color === "#787b86") && firstChart?.contains(this.canvas)) {
        trackedWindow.__crosshairStrokeCount++;
      }
      return path ? originalStroke.call(this, path) : originalStroke.call(this);
    };
    const NativeChannel = window.BroadcastChannel;
    window.BroadcastChannel = class extends NativeChannel {
      constructor(name: string) {
        super(name);
        if (name === "etape.crosshair") this.addEventListener("message", (event) => received.push(event.data));
      }
    };
  });
}

test("syncs a hovered chart across workspace windows and clears on leave", async ({ page }) => {
  const other = await page.context().newPage();
  await trackCrosshairMessages(page);
  await trackCrosshairMessages(other);
  const receiverSymbol = await applyTrading(page, "e2e-crosshair-receiver");
  const sourceSymbol = await applyTrading(other, "e2e-crosshair-source");
  expect(sourceSymbol).toBe(receiverSymbol);

  for (const workspacePage of [page, other]) {
    const toggles = workspacePage.getByRole("button", { name: "Crosshair Sync" });
    for (let i = 0; i < await toggles.count(); i++) {
      await toggles.nth(i).click();
      await expect(toggles.nth(i)).toHaveAttribute("aria-pressed", "true");
    }
  }

  const sourceChart = other.getByTestId("chart-host").first();
  const bounds = await sourceChart.boundingBox();
  if (!bounds) throw new Error("Source chart is not laid out");
  await Promise.all([page, other].map((workspacePage) => workspacePage.evaluate(() => {
    const tracked = window as unknown as { __crosshairReceived: unknown[] };
    tracked.__crosshairReceived.length = 0;
  })));
  await page.evaluate(() => {
    const tracked = window as unknown as { __crosshairInteractions: number; __crosshairStrokeCount: number };
    tracked.__crosshairInteractions = 0;
    tracked.__crosshairStrokeCount = 0;
  });
  await other.mouse.move(bounds.x + bounds.width * 0.65, bounds.y + bounds.height * 0.4);

  await expect.poll(async () => {
    const messages = await page.evaluate(() => (window as unknown as { __crosshairReceived: unknown[] }).__crosshairReceived);
    return messages.some((message) => typeof message === "object" && message !== null
      && "kind" in message && message.kind === "move"
      && "group" in message && message.group === "blue"
      && "symbol" in message && message.symbol === `US.${receiverSymbol}`);
  }, { timeout: 5_000 }).toBe(true);
  await expect.poll(() => page.evaluate(
    () => (window as unknown as { __crosshairStrokeCount: number }).__crosshairStrokeCount,
  ), { timeout: 5_000 }).toBeGreaterThan(0);
  expect(await page.evaluate(() => (window as unknown as { __crosshairInteractions: number }).__crosshairInteractions)).toBe(0);

  await other.mouse.move(0, 0);
  await expect.poll(async () => {
    const messages = await page.evaluate(() => (window as unknown as { __crosshairReceived: unknown[] }).__crosshairReceived);
    return messages.some((message) => typeof message === "object" && message !== null
      && "kind" in message && message.kind === "clear");
  }, { timeout: 5_000 }).toBe(true);
  await other.close();
});
