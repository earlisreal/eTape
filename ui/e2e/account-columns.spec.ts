import { test, expect, type Locator, type Page } from "@playwright/test";

async function expectNoHorizontalOverflow(locator: Locator, name: string): Promise<void> {
  const metrics = await locator.evaluate((table) => ({
    clientWidth: table.parentElement?.clientWidth ?? 0,
    scrollWidth: table.parentElement?.scrollWidth ?? 0,
  }));
  expect(metrics.scrollWidth, `${name} should fit its scroll container`).toBeLessThanOrEqual(metrics.clientWidth);
}

async function addAccountPanel(page: Page): Promise<void> {
  await page.goto("/?workspace=e2e-account-columns");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
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
