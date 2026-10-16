import { test, expect, type Locator, type Page } from "@playwright/test";
import type { ClosedTradeRow, Fill, Order, PositionRow, ProtectionWarning } from "../src/wire/contract";

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
  await expect(page.getByTestId("demo-banner")).toBeVisible();
  await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout:15_000 });
  await page.getByRole("button", { name: "+ Add panel" }).click();
  await page.getByRole("button", { name: /Account Equity, BP, day P&L/ }).last().click();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
}

test("Account keeps protection warnings visible across tabs and clears resolved exposure", async ({ page }) => {
  let warnings: ProtectionWarning[] = [{ symbol:"US.AAPL", qty:2, reason:"Protection interrupted", state:"open" }];
  let publishWarnings: (() => void) | undefined;
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    server.onMessage((message) => {
      if (typeof message !== "string") { socket.send(message); return; }
      const frame = JSON.parse(message);
      if (frame.topic === "exec.status") {
        const publish = () => {
          const venues = frame.payload.venues.map((v: { venue:string }) => ({ ...v, protectionWarnings:v.venue === "sim-paper" ? warnings : [] }));
          socket.send(JSON.stringify({ ...frame, payload:{ ...frame.payload, venues } }));
        };
        publishWarnings = publish;
        publish();
      } else socket.send(message);
    });
  });
  await addAccountPanel(page);
  await page.getByRole("button", { name:"link group", exact:true }).click();
  await page.getByRole("button", { name:"Blue group", exact:true }).click();
  await page.getByTestId("acct-venue").selectOption("sim-paper");
  const alert = page.getByTestId("account-protection-warning");
  await expect(alert).toHaveText("AAPL · 2 shares open · Protection interrupted");
  await page.getByRole("button", { name:"Trade History", exact:true }).click();
  await expect(alert).toBeVisible();
  await page.screenshot({ path:".playwright-mcp/account-protection-warning.png" });
  warnings = [{ symbol:"US.AAPL", qty:0, reason:"Protection interrupted", state:"unverified" }];
  publishWarnings?.();
  await expect(alert).toContainText("Position unverified; retrying automatically");
  warnings = [];
  publishWarnings?.();
  await expect(alert).toHaveCount(0);
});

test("account columns do not create horizontal overflow when they fit", async ({ page }) => {
  await addAccountPanel(page);
  for (const column of ["qty", "price", "stopPrice", "type"]) {
    await expect(page.getByTestId("open-orders-table").locator(`th[data-column="${column}"]`)).toBeVisible();
  }
  await expectNoHorizontalOverflow(page.getByTestId("open-orders-table"), "open orders");
  await expectNoHorizontalOverflow(page.locator("table").filter({ has: page.locator("[data-column='flatten']") }), "positions");

  await page.getByRole("button", { name: "Fills", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("fills-table").locator("table"), "fills");

  await page.getByRole("button", { name: "Trade History", exact: true }).click();
  await expectNoHorizontalOverflow(page.getByTestId("trade-history-table"), "trade history");

  await page.getByTestId("closed-orders-tab").click();
  for (const column of ["qty", "price", "stopPrice", "type"]) {
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
  await expect(stopPrice).toHaveAttribute("aria-valuenow", "68");
  await stopPrice.press("ArrowRight");
  await expect(stopPrice).toHaveAttribute("aria-valuenow", "78");
});

test("account order columns merge limit prices with compact headers and ET dates in both tabs", async ({ page }) => {
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
    await expect(table.locator("thead th")).toHaveText(tab === "open"
      ? ["TIME", "SYM", "SIDE", "QTY", "PRICE", "STOP", "TYPE", "STATE", ""]
      : ["TIME", "SYM", "SIDE", "QTY", "FILLED", "PRICE", "STOP", "TYPE", "AVG FILL", "STATE", "REASON"]);
    await expect(table.locator("[data-column='stopLimitPrice']")).toHaveCount(0);
    await expect(table.locator("thead th").first()).toHaveAttribute("title", tab === "open" ? "Submitted (US Eastern)" : "Closed (US Eastern)");
    const stopLimit = table.locator("tbody tr").filter({ hasText: "STPLMT" });
    for (const [column, text] of [["qty", tab === "open" ? "60" : "100"], ["price", "2.370"], ["stopPrice", "2.070"]]) {
      await expect(stopLimit.locator(`[data-column="${column}"]`)).toHaveText(text);
    }
    const timeColumn = tab === "open" ? "createdMs" : "updatedMs";
    await expect(stopLimit.locator(`[data-column="${timeColumn}"]`)).toHaveText("09:30:00");
    const market = table.locator("tbody tr").filter({ hasText: "MKT" });
    await expect(market.locator(`[data-column="${timeColumn}"]`)).toHaveText("09/29 09:30:00");
    for (const column of ["price", "stopPrice"]) await expect(market.locator(`[data-column="${column}"]`)).toHaveText("—");
    await expectNoHorizontalOverflow(table, `${tab} orders with historical dates`);
    await table.screenshot({ path: `.playwright-mcp/account-price-${tab}-orders.png` });
  }
});

test("sub-dollar prices fit all Account tables at normal and minimum column widths", async ({ page }, testInfo) => {
  const now = Date.now();
  const order: Order = {
    venue: "sim-paper", id: "penny-order", symbol: "US.PENNY", side: "BUY", type: "STOP_LIMIT", tif: "DAY", session: "AUTO",
    qty: 100, leavesQty: 60, executedQty: 40, limitPrice: 0.1234, stopPrice: 0.12, avgFillPrice: 0.1205,
    status: "PARTIALLY_FILLED", rejectReason: "", replacesId: "", createdMs: now, updatedMs: now,
  };
  const position: PositionRow = { venue: "sim-paper", symbol: "US.PENNY", qty: 100, avgPrice: 0.1234, unrealizedPnl: 0.25, dayBasis: 0.1234, openedMs: now };
  const fill: Fill = { venue: "sim-paper", orderId: order.id, symbol: order.symbol, side: "BUY", qty: 40, price: 0.1205, tsMs: now };
  const trade: ClosedTradeRow = { venue: "sim-paper", symbol: order.symbol, qty: 100, isLong: true,
    entryPrice: 0.1234, exitPrice: 0.1259, realized: 0.25, openMs: now - 1000, closeMs: now, seq: 1 };
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    server.onMessage((message) => {
      if (typeof message !== "string") { socket.send(message); return; }
      const frame = JSON.parse(message);
      if (frame.kind === "snapshot") {
        if (frame.topic === "exec.positions") frame.payload = [position];
        if (frame.topic === "exec.orders") {
          frame.payload = [order];
          socket.send(JSON.stringify({ kind: "snapshot", topic: "exec.fills", payload: [fill] }));
          socket.send(JSON.stringify({ kind: "snapshot", topic: "exec.trades", payload: [trade] }));
        }
        if (frame.topic === "exec.closedOrders") frame.payload = [{ ...order, status: "CANCELED" }];
        if (frame.topic === "exec.trades") frame.payload = [trade];
      }
      socket.send(JSON.stringify(frame));
    });
  });
  await addAccountPanel(page);
  await page.getByRole("button", { name: "link group", exact: true }).click();
  await page.getByRole("button", { name: "Blue group", exact: true }).click();
  await page.getByTestId("acct-venue").selectOption("sim-paper");

  const expectPricesFit = async (table: Locator, prices: Record<string, string>, screenshot: string) => {
    for (const [column, text] of Object.entries(prices)) {
      const cell = table.locator(`tbody [data-column="${column}"]`).first();
      await expect(cell).toHaveText(text);
      await cell.scrollIntoViewIfNeeded();
      await expect(cell).toBeInViewport();
      expect(await cell.evaluate((el) => el.scrollWidth <= el.clientWidth), `${column} text should fit its column`).toBe(true);
    }
    await page.screenshot({ path: testInfo.outputPath(screenshot) });
  };
  for (const width of [1280, 360]) {
    await page.setViewportSize({ width, height: 900 });
    await page.getByTestId("open-orders-tab").click();
    await expectPricesFit(page.getByTestId("open-orders-table"), { price: "0.1234", stopPrice: "0.1200" }, `sub-dollar-${width}-open.png`);
    await page.getByTestId("closed-orders-tab").click();
    await expectPricesFit(page.getByTestId("closed-orders-table"), { price: "0.1234", stopPrice: "0.1200", avgFillPrice: "0.1205" }, `sub-dollar-${width}-closed.png`);
    await page.getByRole("button", { name: /^Positions/ }).click();
    await expectPricesFit(page.locator("table").filter({ has: page.locator("[data-column='flatten']") }), { avgPrice: "0.1234" }, `sub-dollar-${width}-positions.png`);
    await page.getByRole("button", { name: "Fills", exact: true }).click();
    await expectPricesFit(page.getByTestId("fills-table").locator("table"), { price: "0.1205" }, `sub-dollar-${width}-fills.png`);
    await page.getByRole("button", { name: "Trade History", exact: true }).click();
    await expectPricesFit(page.getByTestId("trade-history-table"), { entryPrice: "0.1234", exitPrice: "0.1259", diff: "0.0025" }, `sub-dollar-${width}-history.png`);
    await expect(page.getByTestId("trade-history-table").locator('tbody [data-column="realized"]')).toHaveText("0.25");
  }
});
