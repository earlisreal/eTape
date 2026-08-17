import { test, expect, type Locator, type Page } from "@playwright/test";

async function expectNoHorizontalOverflow(locator: Locator, name: string): Promise<void> {
  const metrics = await locator.evaluate((table) => ({
    clientWidth: table.parentElement?.clientWidth ?? 0,
    scrollWidth: table.parentElement?.scrollWidth ?? 0,
  }));
  expect(metrics.scrollWidth, `${name} should fit its scroll container`).toBeLessThanOrEqual(metrics.clientWidth);
}

async function addAccountPanel(page: Page, waitForLatency = true): Promise<void> {
  await page.goto("/?workspace=e2e-account-columns");
  if (waitForLatency) await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "+ Add panel" }).click();
  await page.getByRole("button", { name: /Account Equity, BP, day P&L/ }).last().click();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
}

test("account columns do not create horizontal overflow when they fit", async ({ page }) => {
  await addAccountPanel(page);
  await expectNoHorizontalOverflow(page.getByTestId("open-orders-table"), "open orders");
  await expectNoHorizontalOverflow(page.locator("table").filter({ has: page.locator("[data-column='flatten']") }), "positions");

  await page.getByRole("button", { name: "Fills", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("fills-table").locator("table"), "fills");

  await page.getByRole("button", { name: "Trade History", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("trade-history-table"), "trade history");

  await page.getByTestId("closed-orders-tab").click();
  await expectNoHorizontalOverflow(page.getByTestId("closed-orders-table"), "closed orders");
});

test("account columns remain adjustable when a narrow panel starts at minimum widths", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await addAccountPanel(page, false);

  const handle = page.getByTestId("positions-resize-symbol");
  await expect(handle).toHaveAttribute("aria-valuenow", "68");
  const box = await handle.boundingBox();
  if (!box) throw new Error("positions resize handle is not visible");

  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 32, box.y + box.height / 2);
  await page.mouse.up();

  await expect(handle).toHaveAttribute("aria-valuenow", "100");
});
