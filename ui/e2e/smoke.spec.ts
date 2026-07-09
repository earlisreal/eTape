import { test, expect, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";

const ART = "e2e/.artifacts";
mkdirSync(ART, { recursive: true });
const shot = (page: Page, name: string) => page.screenshot({ path: `${ART}/${name}.png`, fullPage: true });

// Blank-start workspace model (Task 7/10): `?workspace=<name>` always starts
// empty (no auto-seed). Reaching a populated layout means applying a preset
// card from the empty-state Catalog. Each helper below uses a workspace name
// unique to its caller so parallel-looking test runs never bleed state into
// each other (the whole suite shares one long-lived replay engine + config
// store for the run — see e2e/serve.sh).
async function gotoAndApplyPreset(page: Page, workspace: string, presetName: "Trading" | "Monitoring"): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  // The empty-state->preset transition races the async workspace load; wait
  // for the top bar (always mounted once the workspace doc resolves, even
  // when it's blank) before clicking a preset card.
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: new RegExp(`^${presetName}`) }).click();
}

test.describe("trading workspace", () => {
  test("panels mount and the account bar hydrates", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-trading", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("side-BUY")).toBeVisible(); // order ticket mounted
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
    await gotoAndApplyPreset(page, "e2e-trading-order", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // Two-layer gate: arm master, then the venue.
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("ARMED");
    await page.getByTestId("venue-arm-sim-paper").click();
    await expect(page.getByTestId("venue-arm-sim-paper")).toHaveAttribute("data-armed", "true");

    // MARKET order needs no price; default 100 shares of AAPL fills at the mark.
    await page.getByTestId("order-type").selectOption("MARKET");
    await page.getByTestId("side-BUY").click();

    await expect(page.getByText("Filled").first()).toBeVisible({ timeout: 10_000 }); // OpenOrders status
    await page.waitForTimeout(500); // let the next rAF paint tick pick up the fill (canvas, not React state)
    await shot(page, "trading-filled"); // eyeball: fill diamond on the chart
  });
});

test.describe("monitoring workspace", () => {
  test("loads; scanner/news show their empty state (no pollers in replay)", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-monitoring", "Monitoring");
    // Charts are canvas; assert a deterministic empty-state text + screenshot.
    // Matches ScannerPanel.tsx ("No symbols match the current filters.") /
    // StockInfoPanel.tsx ("Stock Info · no symbol focused").
    await expect(page.getByText(/no symbols match|no symbol focused/i).first()).toBeVisible({ timeout: 15_000 });
    await page.screenshot({ path: "e2e/.artifacts/monitoring-loaded.png", fullPage: true });
  });
});

test.describe("link groups", () => {
  // Task 9/13 removed the old top-bar symbol-box-per-group link controls in
  // favor of type-to-load: click a symbol-bearing panel to make it dockview's
  // active panel, then type a ticker straight into its header (no input to
  // focus) and press Enter to commit. Committing on a grouped (non-pinned)
  // panel moves the whole link group, which every other panel in that group
  // picks up live via LinkGroups.subscribe.
  test("typing a symbol into a blue-group panel's header moves the whole group", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-trading-link", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // The Trading preset's "t-dom" (DOM Ladder) and "t-tape" (Time & Sales)
    // panels are both in the blue group alongside the two charts and the
    // order ticket. Every leaf in TRADING_LAYOUT is its own always-visible
    // dockview group (no tabs to switch), so panel-symbol/ledger-header text
    // uniquely scopes each panel's header despite the shared data-testid.
    const domHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
    const domPanel = domHeader.locator("xpath=..");
    const tapeHeader = page.locator(".ledger-header", { hasText: "Time & Sales" });
    const tapeSymbol = tapeHeader.getByTestId("panel-symbol");

    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("AAPL"); // preset default
    await expect(tapeSymbol).toHaveText("AAPL");

    // Click the DOM ladder's body (not the header's link-group swatch/close
    // buttons) to make it dockview's active panel, per PanelFrame's
    // active-via-api tracking.
    await domPanel.getByTestId("panel-body").click();
    await expect(domPanel).toHaveClass(/panel-focused/);

    await page.keyboard.type("NVDA");
    await page.keyboard.press("Enter");

    // The header renders the bare symbol (no "US." market prefix, per
    // PanelFrame's bareSymbol formatting) — assert the tape panel (a
    // DIFFERENT blue-group panel) picked up the new focus, proving the
    // group-follow path, not just local echo.
    await expect(tapeSymbol).toHaveText("NVDA", { timeout: 10_000 });
    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("NVDA");
    await shot(page, "link-focus-nvda");
  });
});

test.describe("sortable tables", () => {
  test("a column sort survives a page reload", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-sort", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    const header = page.getByRole("columnheader", { name: /Unrl P&L/ });
    await expect(header).toHaveText(/▾/); // AccountPanel's DEFAULT_SORT: unrealizedPnl desc
    await header.click(); // toggles the already-active column: desc -> asc
    await expect(header).toHaveText(/▴/);

    // WorkspaceStore.save() debounces the SetConfig write by 500ms; give it
    // room to flush before reloading, or the sort choice below would race an
    // in-flight (or not-yet-scheduled) persist.
    await page.waitForTimeout(800);
    await page.reload();

    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole("columnheader", { name: /Unrl P&L/ })).toHaveText(/▴/);
  });
});

test.describe("preset apply confirmation", () => {
  test("re-applying a preset onto a populated workspace confirms the replace", async ({ page }) => {
    let dialogSeen = false;
    page.on("dialog", (d) => { dialogSeen = true; void d.accept(); });

    await gotoAndApplyPreset(page, "e2e-preset-confirm", "Monitoring");
    await expect(page.getByText(/no symbols match|no symbol focused/i).first()).toBeVisible({ timeout: 15_000 });

    // AppShell.applyPresetToWorkspace only confirms when the workspace already
    // has panels; trigger a second preset apply via the "+ Add panel" popover
    // (the same Catalog component the empty-state renders).
    await page.getByRole("button", { name: "+ Add panel" }).click();
    await page.getByRole("button", { name: /^Trading/ }).click();

    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 }); // Trading now applied
    expect(dialogSeen).toBe(true);
  });
});
