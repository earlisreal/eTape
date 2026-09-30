import { test, expect, type Locator, type Page } from "@playwright/test";
import type { Order } from "../src/wire/contract";

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
  for (const column of ["qty", "price", "stopPrice", "stopLimitPrice", "type"]) {
    await expect(page.getByTestId("open-orders-table").locator(`th[data-column="${column}"]`)).toBeVisible();
  }
  await expectNoHorizontalOverflow(page.getByTestId("open-orders-table"), "open orders");
  await expectNoHorizontalOverflow(page.locator("table").filter({ has: page.locator("[data-column='flatten']") }), "positions");

  await page.getByRole("button", { name: "Fills", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("fills-table").locator("table"), "fills");

  await page.getByRole("button", { name: "Trade History", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("trade-history-table"), "trade history");

  await page.getByTestId("closed-orders-tab").click();
  for (const column of ["qty", "price", "stopPrice", "stopLimitPrice", "type"]) {
    await expect(page.getByTestId("closed-orders-table").locator(`th[data-column="${column}"]`)).toBeVisible();
  }
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

  const orders = page.getByTestId("open-orders-table");
  const overflow = await orders.evaluate((table) => table.parentElement!.scrollWidth > table.parentElement!.clientWidth);
  expect(overflow).toBe(true);
  const stopPrice = page.getByTestId("open-orders-resize-stopPrice");
  await expect(stopPrice).toHaveAttribute("aria-valuenow", "92");
  await stopPrice.press("ArrowRight");
  await expect(stopPrice).toHaveAttribute("aria-valuenow", "102");
});

test("account order columns show separate prices and compact ET dates in both tabs", async ({ page }) => {
  const today = Date.parse("2026-09-30T13:30:00Z");
  await page.addInitScript((now) => { Date.now = () => now; }, today);
  const base: Order = {
    venue: "sim-paper", id: "limit", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
    qty: 100, leavesQty: 60, executedQty: 40, limitPrice: 2.07, stopPrice: 0, avgFillPrice: 2.065,
    status: "PARTIALLY_FILLED", rejectReason: "", replacesId: "", createdMs: today, updatedMs: today,
  };
  const orders: Order[] = [
    base,
    { ...base, id: "stop", type: "STOP", stopPrice: 2.07, createdMs: today + 1 },
    { ...base, id: "stop-limit", type: "STOP_LIMIT", stopPrice: 2.07, limitPrice: 2.37, createdMs: today + 2 },
    { ...base, id: "market", type: "MARKET", limitPrice: 999, stopPrice: 999, createdMs: today - 86_400_000, updatedMs: today - 86_400_000 },
  ];
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    server.onMessage((message) => {
      if (typeof message !== "string") { socket.send(message); return; }
      const frame = JSON.parse(message);
      if (frame.kind === "snapshot" && frame.topic === "exec.orders") frame.payload = orders;
      if (frame.kind === "snapshot" && frame.topic === "exec.closedOrders") frame.payload = orders.map((o) => ({ ...o, status: "CANCELED" }));
      socket.send(JSON.stringify(frame));
    });
  });
  await addAccountPanel(page);
  await page.getByRole("button", { name: "link group", exact: true }).click();
  await page.getByRole("button", { name: "Blue group", exact: true }).click();
  await page.getByTestId("acct-venue").selectOption("sim-paper");

  for (const tab of ["open", "closed"] as const) {
    await page.getByTestId(`${tab}-orders-tab`).click();
    const table = page.getByTestId(`${tab}-orders-table`);
    await expect(table.locator("tbody tr")).toHaveCount(4);
    const stopLimit = table.locator("tbody tr").filter({ hasText: "STPLMT" });
    for (const [column, text] of [["qty", tab === "open" ? "60" : "100"], ["price", "—"], ["stopPrice", "2.070"], ["stopLimitPrice", "2.370"]]) {
      await expect(stopLimit.locator(`[data-column="${column}"]`)).toHaveText(text);
    }
    const timeColumn = tab === "open" ? "createdMs" : "updatedMs";
    await expect(stopLimit.locator(`[data-column="${timeColumn}"]`)).toHaveText("09:30:00");
    const market = table.locator("tbody tr").filter({ hasText: "MKT" });
    await expect(market.locator(`[data-column="${timeColumn}"]`)).toHaveText("09/29 09:30:00");
    for (const column of ["price", "stopPrice", "stopLimitPrice"]) await expect(market.locator(`[data-column="${column}"]`)).toHaveText("—");
    await expectNoHorizontalOverflow(table, `${tab} orders with historical dates`);
    await page.getByTestId("orders-table").screenshot({ path: `.playwright-mcp/account-${tab}-orders.png` });
  }
});
