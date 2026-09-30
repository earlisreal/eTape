import { test, expect, type Page } from "@playwright/test";

async function gotoTrading(page: Page, workspace: string): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
  await page.getByRole("button", { name: /^Trading/ }).click();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
  const ladderHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
  const ladder = page.getByRole("region", { name: "ladder" });
  await ladder.getByTestId("panel-body").click();
  await page.keyboard.type("DRGO");
  await page.keyboard.press("Enter");
  await expect(ladderHeader.getByTestId("panel-symbol")).toHaveText("DRGO", { timeout: 10_000 });
  await page.keyboard.type("GLDN");
  await page.keyboard.press("Enter");
  await expect(ladderHeader.getByTestId("panel-symbol")).toHaveText("GLDN", { timeout: 10_000 });
}

async function ask(page: Page): Promise<number> {
  await expect.poll(async () => Number((await page.getByTestId("ask").innerText()).trim()), { timeout: 10_000 }).toBeGreaterThan(0);
  return Number((await page.getByTestId("ask").innerText()).trim());
}

test("demo held stop-limit marker cancels, triggers, and fills its linked limit child", async ({ page }) => {
  const workspace = `e2e-stop-limit-${Math.random().toString(36).slice(2)}`;
  await gotoTrading(page, workspace);

  await page.getByTestId("arm-chip").click();
  await expect(page.getByTestId("arm-chip")).toHaveText("LOCK TRADING");

  const venue = page.getByTestId("venue");
  await venue.selectOption("sim-paper");
  await expect(venue).toHaveValue("sim-paper");
  const orderType = page.getByTestId("order-type");
  const session = page.getByTestId("session");
  await orderType.selectOption("STOP_LIMIT");
  await expect(orderType).toHaveValue("STOP_LIMIT");
  await session.selectOption("EXTENDED");
  await expect(session).toHaveValue("EXTENDED");
  const custody = page.getByTestId("stop-limit-custody-preview");
  await expect(custody).not.toContainText("Checking stop-limit custody");
  // ponytail: demo uses the host clock; add a controllable engine clock if this scenario needs to run during RTH.
  if (!(await custody.innerText()).includes("LOCAL · eTape-held")) {
    test.skip(true, "Engine-held stop-limits are available only during pre- or post-market.");
  }

  const limitPrice = Number((await ask(page) + 0.5).toFixed(2));
  await page.getByTestId("amount").fill("1");
  await page.getByTestId("price").fill(limitPrice.toFixed(2));
  await page.getByTestId("stop").fill(limitPrice.toFixed(2));
  await expect(page.getByTestId("amount")).toHaveValue("1");
  await expect(page.getByTestId("price")).toHaveValue(limitPrice.toFixed(2));
  await expect(page.getByTestId("stop")).toHaveValue(limitPrice.toFixed(2));
  await page.getByTestId("side-BUY").click();
  const stopGroups = page.locator('[data-order-group][data-kind="stop-limit"]');
  await expect(stopGroups.first()).toHaveCount(1);
  const cancelStop = page.locator('button[aria-label="Cancel BUY 1 STOP-LIMIT"]:visible').first();
  await expect(cancelStop).toBeVisible();

  await cancelStop.click();
  await expect(stopGroups).toHaveCount(0);

  const restingChildPrice = Number((await ask(page) * 0.95).toFixed(2));
  await page.getByTestId("price").fill(restingChildPrice.toFixed(2));
  await page.getByTestId("stop").fill("1");
  await page.getByTestId("side-BUY").click();

  const limitGroups = page.locator('[data-order-group][data-kind="limit"]');
  await expect(limitGroups.first()).toHaveCount(1, { timeout: 15_000 });
  const cancelChild = page.locator('button[aria-label="Cancel BUY 1 LIMIT"]:visible').first();
  await expect(cancelChild).toBeVisible();
  await cancelChild.click();
  await expect(limitGroups).toHaveCount(0);

  const marketableLimit = Number((await ask(page) + 5).toFixed(2));
  await page.getByTestId("price").fill(marketableLimit.toFixed(2));
  await page.getByTestId("stop").fill("1");
  await page.getByTestId("side-BUY").click();
  await page.getByTestId("closed-orders-tab").click();
  await expect(page.getByTestId("closed-orders-table")).toContainText("Filled", { timeout: 15_000 });
});
