import { test, expect, type Page } from "@playwright/test";

async function gotoTrading(page: Page, workspace: string): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
  await page.getByRole("button", { name: /^Trading/ }).click();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
  await focusLadderSymbol(page, "DRGO");
  await focusLadderSymbol(page, "GLDN");
}

async function focusLadderSymbol(page: Page, symbol: string): Promise<void> {
  const ladderHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
  const ladder = page.getByRole("region", { name: "ladder" });
  await ladder.getByTestId("panel-body").click();
  await page.keyboard.type(symbol);
  await page.keyboard.press("Enter");
  await expect(ladderHeader.getByTestId("panel-symbol")).toHaveText(symbol, { timeout: 10_000 });
}

async function ensureArmed(page: Page): Promise<void> {
  const armChip = page.getByTestId("arm-chip");
  if ((await armChip.innerText()).trim() !== "LOCK TRADING") await armChip.click();
  await expect(armChip).toHaveText("LOCK TRADING");
}

async function ask(page: Page): Promise<number> {
  await expect.poll(async () => Number((await page.getByTestId("ask").innerText()).trim()), { timeout: 10_000 }).toBeGreaterThan(0);
  return Number((await page.getByTestId("ask").innerText()).trim());
}

test("demo held stop-limit marker cancels, triggers, and fills its linked limit child", async ({ page }) => {
  const workspace = `e2e-stop-limit-${Math.random().toString(36).slice(2)}`;
  await gotoTrading(page, workspace);

  await ensureArmed(page);

  const venue = page.getByTestId("venue");
  await venue.selectOption("sim-paper");
  await expect(venue).toHaveValue("sim-paper");
  const orderType = page.getByTestId("order-type");
  const session = page.getByTestId("session");
  await orderType.selectOption("STOP_LIMIT");
  await expect(orderType).toHaveValue("STOP_LIMIT");
  await session.selectOption("EXTENDED");
  await expect(session).toHaveValue("EXTENDED");
  const custody = page.getByTestId("conditional-custody-preview");
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

test("demo held LIT activates one LIMIT child, then cancels or fills it", async ({ page }) => {
  const workspace = `e2e-lit-${Math.random().toString(36).slice(2)}`;
  await gotoTrading(page, workspace);
  await ensureArmed(page);
  await focusLadderSymbol(page, "DRGO"); // isolated from the stop-limit scenario's GLDN state

  const venue = page.getByTestId("venue");
  await venue.selectOption("sim-paper");
  const orderType = page.getByTestId("order-type");
  await orderType.selectOption("LIMIT_IF_TOUCHED");
  const custody = page.getByTestId("conditional-custody-preview");
  await expect(custody).not.toContainText("Checking LIT custody");
  await expect(custody).toContainText("Last-Eligible price");
  if (!(await custody.innerText()).includes("LOCAL · eTape-held")) {
    test.skip(true, "Engine-held LIT is unavailable outside PRE/RTH/POST sessions.");
  }

  const lastEligible = Number((await custody.innerText()).match(/Last-Eligible price (\d+(?:\.\d+)?)/)?.[1]);
  const untouchedTrigger = Number(Math.max(0.01, lastEligible - Math.max(0.05, lastEligible * 0.5)).toFixed(2));
  await page.getByTestId("amount").fill("1");
  await page.getByTestId("price").fill("0.01");
  await page.getByTestId("stop").fill(untouchedTrigger.toFixed(2));
  await expect(custody).not.toContainText("Will trigger now for BUY/COVER");
  await page.getByTestId("side-BUY").click();

  const limitGroups = page.getByTestId("chart-host").first().locator('[data-order-group][data-kind="limit"]');
  await expect(limitGroups).toHaveCount(0);
  const cancelParent = page.locator('button[aria-label="Cancel BUY 1 LIT"]:visible').first();
  await expect(cancelParent).toBeVisible();
  await cancelParent.click();
  await expect(cancelParent).toHaveCount(0);
  await expect(limitGroups).toHaveCount(0);

  const trigger = Number((await ask(page) + 1).toFixed(2));
  await page.getByTestId("stop").fill(trigger.toFixed(2));
  await page.getByTestId("side-BUY").click();
  await expect(limitGroups).toHaveCount(1, { timeout: 15_000 });
  const cancelChild = page.locator('button[aria-label="Cancel BUY 1 LIT"]:visible').first();
  await expect(cancelChild).toBeVisible();
  await cancelChild.click();
  await expect(limitGroups).toHaveCount(0, { timeout: 10_000 });

  const marketableLimit = Number((await ask(page) + 20).toFixed(2));
  const refreshedTrigger = Number((await ask(page) + 1).toFixed(2));
  await page.getByTestId("price").fill(marketableLimit.toFixed(2));
  await page.getByTestId("stop").fill(refreshedTrigger.toFixed(2));
  await page.getByTestId("side-BUY").click();
  await page.getByTestId("closed-orders-tab").click();
  const drgoRows = page.getByTestId("closed-orders-table").locator("tbody tr").filter({
    has: page.locator('[data-column="symbol"]', { hasText: "DRGO" }),
  });
  const filledLit = drgoRows.filter({ hasText: "LIT" }).filter({ has: page.getByText("Filled") });
  await expect(filledLit).toHaveCount(1, { timeout: 15_000 });

  await expect(page.getByTestId("flatten-sim-paper-US.DRGO")).toBeVisible({ timeout: 10_000 });
  await page.getByTestId("price").fill("0.01");
  await page.getByTestId("stop").fill("0.01");
  await page.getByTestId("side-SELL").click();
  const filledSellLit = drgoRows.filter({ hasText: "LIT" })
    .filter({ has: page.locator('[data-column="side"]', { hasText: "SELL" }) })
    .filter({ has: page.getByText("Filled") });
  await expect(filledSellLit).toHaveCount(1, { timeout: 15_000 });
  await expect(page.getByTestId("flatten-sim-paper-US.DRGO")).toHaveCount(0, { timeout: 10_000 });
});
