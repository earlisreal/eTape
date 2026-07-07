import { test, expect, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";

const ART = "e2e/.artifacts";
mkdirSync(ART, { recursive: true });
const shot = (page: Page, name: string) => page.screenshot({ path: `${ART}/${name}.png`, fullPage: true });

test.describe("trading workspace", () => {
  test("panels mount and the account bar hydrates", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("submit")).toBeVisible(); // order ticket mounted
    await shot(page, "trading-loaded"); // eyeball: charts populated + ladder painted (canvas)
  });

  test("a paper MARKET order walks to Filled and paints a fill diamond", async ({ page }) => {
    // preChecks.ts coerces MARKET->LIMIT-at-last outside real-wall-clock RTH
    // (client-side safety rule, independent of the replay day's simulated
    // clock — the engine itself has no RTH gate, verified in exec/gate.go and
    // broker/sim/sim.go). Pin Date.now() to a weekday RTH instant so the
    // order actually submits as MARKET and crosses the spread immediately,
    // instead of sitting as an unfilled limit at the (non-marketable) last
    // price. Wed 2026-07-08T15:00:00Z = 11:00 ET.
    await page.addInitScript(() => { Date.now = () => 1_783_522_800_000; });
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // Two-layer gate: arm master, then the venue.
    await page.getByTestId("arm-toggle").click();
    await expect(page.getByTestId("arm-toggle")).toHaveText("ARMED");
    await page.getByTestId("venue-arm-sim-paper").click();
    await expect(page.getByTestId("venue-arm-sim-paper")).toHaveAttribute("data-armed", "true");

    // MARKET order needs no price; default 100 shares of AAPL fills at the mark.
    await page.getByTestId("order-type").selectOption("MARKET");
    await page.getByTestId("submit").click();

    await expect(page.getByText("Filled").first()).toBeVisible({ timeout: 10_000 }); // OpenOrders status
    await page.waitForTimeout(500); // let the next rAF paint tick pick up the fill (canvas, not React state)
    await shot(page, "trading-filled"); // eyeball: fill diamond on the chart
  });
});

test.describe("monitoring workspace", () => {
  test("loads; scanner/news show their empty state (no pollers in replay)", async ({ page }) => {
    await page.goto("/?workspace=monitoring");
    // Charts are canvas; assert a deterministic empty-state text + screenshot.
    // Confirm the exact copy in ScannerPanel.tsx / NewsPanel.tsx and tighten this regex.
    await expect(page.getByText(/no symbols match|no symbol focused/i).first()).toBeVisible({ timeout: 15_000 });
    await page.screenshot({ path: "e2e/.artifacts/monitoring-loaded.png", fullPage: true });
  });
});

test.describe("link groups", () => {
  test("focusing the green group moves its panels together", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    // Four identical symbol inputs exist (one per group); target green by aria-label.
    const green = page.getByLabel("focus green");
    await green.fill("NVDA");
    await green.press("Enter");
    // The order ticket is in the green group; its symbol label follows the focus.
    await expect(page.getByText("NVDA", { exact: false }).first()).toBeVisible({ timeout: 10_000 });
    await page.screenshot({ path: "e2e/.artifacts/link-focus-nvda.png", fullPage: true });
  });
});
