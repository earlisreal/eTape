import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { createServer } from "vite";

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), "chart-order-layout");
const server = await createServer({ configFile: false, root, esbuild: { jsx: "automatic" }, server: {
  host: "127.0.0.1", port: 0, fs: { allow: [path.resolve(root, "../..") ] },
} });
let browser;
try {
  await server.listen();
  const { port } = server.httpServer.address();
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1000, height: 800 } });
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(`http://127.0.0.1:${port}`);
  await page.waitForFunction(() => window.repro?.ready);
  await page.waitForTimeout(100);
  const measure = () => page.evaluate(() => window.repro.measure(280));
  const states = [await measure()];

  await page.evaluate(() => window.repro.setZoom({ from: 10, to: 70 }));
  const zoomed = await measure();
  assert.notDeepEqual(zoomed.range, states[0].range, "The scenario must exercise a manually zoomed chart");
  await page.evaluate(() => window.repro.resizePanel(640, 520));
  await page.waitForFunction(() => document.querySelector("[data-testid='chart-host']").clientWidth === 640);
  await page.waitForTimeout(100);
  const baseline = await measure();
  states.push(zoomed, baseline);

  for (let cycle = 0; cycle < 2; cycle++) {
    await page.keyboard.down("Shift");
    await page.mouse.move(350, 280);
    await page.waitForFunction(() => document.querySelector('[data-testid="chart-order-entry-preview"]').style.opacity === "1");
    const preview = await measure();
    if (cycle === 0) await page.waitForTimeout(350); // Let the 250ms route preview expire before clicking.
    await page.mouse.click(350, 280);
    await page.keyboard.up("Shift");
    await page.locator("[data-order-group]").waitFor({ state: "attached" });
    const submitted = await measure();
    await page.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
    await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
    const canceled = await measure();
    states.push(preview, submitted, canceled);
    assert.equal(preview.actualStop, preview.expectedStop, `Cycle ${cycle + 1}: preview STOP must match cursor price`);
    assert(Math.abs(preview.stopDrawnY - 280) <= 1, `Cycle ${cycle + 1}: snapped STOP must remain under cursor`);
    if (cycle === 1) {
      assert.equal(submitted.submittedStop, Number(preview.previewStopText), "Submitted STOP must match its displayed preview");
      assert(Math.abs(submitted.submittedLimit - submitted.submittedStop - 0.05) < 0.001, "LIMIT must retain the template's $0.05 cushion");
      assert(preview.previewDetailText.includes("→ limit 5.12"), "Preview must show the cushion-adjusted LIMIT");
    }
  }

  await page.evaluate(() => {
    window.repro.addOrder("layout-limit", "LIMIT", 4.7);
    window.repro.addOrder("layout-stop", "STOP_LIMIT", 5.1);
  });
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 2);
  const multipleOrders = await measure();
  const kinds = await page.locator("[data-order-group]").evaluateAll(nodes => nodes.map(node => node.dataset.kind).sort());
  assert.deepEqual(kinds, ["limit", "stop-limit"], "Both LIMIT and STOP_LIMIT overlays must be covered");
  states.push(multipleOrders);
  const limitLabel = page.getByTestId("order-label-layout-limit");
  const limitBox = await limitLabel.boundingBox();
  const dragX = limitBox.x + limitBox.width / 2, dragY = limitBox.y + limitBox.height / 2;
  await page.mouse.move(dragX, dragY);
  await page.mouse.down();
  await page.mouse.move(dragX, dragY - 16);
  await page.mouse.up();
  await page.waitForFunction(() => window.repro.lastReplace?.orderId === "layout-limit"
    && Number(document.querySelector('[data-order-ids="layout-limit"]')?.dataset.price) !== 4.7);
  const replacement = await measure();
  const replaceArgs = await page.evaluate(() => window.repro.lastReplace);
  assert.equal(replaceArgs.qty, 0, "Chart replacement must remain a price-only edit");
  states.push(replacement);
  await page.getByRole("button", { name: "Cancel BUY 1 LIMIT" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 1);
  states.push(await measure());
  await page.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  states.push(await measure());

  await page.evaluate(() => window.repro.addOrder("pending-cancel", "LIMIT", 5.0));
  await page.locator('[data-testid="order-label-pending-cancel"]').waitFor();
  await page.evaluate(() => window.repro.setNextCancelBehavior("pending"));
  await page.getByRole("button", { name: "Cancel BUY 1 LIMIT" }).click();
  await page.waitForFunction(() => document.querySelector('[aria-label="Cancel BUY 1 LIMIT"]')?.disabled);
  assert.equal(await page.locator("[data-order-group]").count(), 1, "Pending cancel must retain the marker");
  states.push(await measure());
  await page.evaluate(() => window.repro.resolvePendingCancel());
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);

  await page.evaluate(() => window.repro.addOrder("rejected-cancel", "LIMIT", 5.0));
  await page.locator('[data-testid="order-label-rejected-cancel"]').waitFor();
  await page.evaluate(() => window.repro.setNextCancelBehavior("rejected"));
  await page.getByRole("button", { name: "Cancel BUY 1 LIMIT" }).click();
  await page.waitForFunction(() => document.querySelector(".chart-order-announcement")?.textContent.includes("Cancel rejected"));
  assert.equal(await page.locator("[data-order-group]").count(), 1, "Rejected cancel must retain the marker");
  states.push(await measure());
  await page.evaluate(() => window.repro.finishOrder("rejected-cancel"));
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);

  await page.evaluate(() => window.repro.addOrder("unknown-cancel", "LIMIT", 5.0));
  await page.locator('[data-testid="order-label-unknown-cancel"]').waitFor();
  await page.evaluate(() => window.repro.setNextCancelBehavior("unknown"));
  await page.getByRole("button", { name: "Cancel BUY 1 LIMIT" }).click();
  await page.waitForFunction(() => document.querySelector(".chart-order-announcement")?.textContent.includes("Cancel outcome unknown"));
  assert.equal(await page.locator("[data-order-group]").count(), 1, "Unknown cancel must retain the marker");
  states.push(await measure());
  await page.evaluate(() => window.repro.finishOrder("unknown-cancel"));
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  states.push(await measure());

  assert.equal(errors.length, 0, errors.join("\n"));
  assert(states.every(state => state.nativeOffset === 0), "Order changes must keep the native chart at the host origin");
  assert(states.every(state => state.axisClippedPx === 0), "Order changes must keep the full time axis visible");
  assert(states.every(state => state.panelAxisClippedPx === 0), "Order changes must keep the full time axis inside the panel body");
  const orderStates = states.slice(2);
  assert(orderStates.every(state => JSON.stringify(state.range) === JSON.stringify(baseline.range)), "Order changes must preserve the manually zoomed time viewport");
  assert(orderStates.every(state => Math.abs(state.candleY - baseline.candleY) <= 1), "Order changes must preserve the price scale");

  const productionPage = await browser.newPage({ viewport: { width: 1000, height: 800 } });
  const productionErrors = [];
  productionPage.on("pageerror", error => productionErrors.push(error.message));
  await productionPage.goto(`http://127.0.0.1:${port}/?production`);
  await productionPage.waitForFunction(() => window.chartPanelProbe?.isReady(), { timeout: 5000 }).catch(async error => {
    console.log("production startup diagnostic", await productionPage.evaluate(() => ({
      readyProbe: typeof window.chartPanelProbe, body: document.body.innerText.slice(0, 800),
      chartHost: !!document.querySelector('[data-testid="chart-host"]'), nativeChart: !!document.querySelector(".tv-lightweight-charts"),
    })));
    throw error;
  });
  await productionPage.waitForFunction(() => {
    const host = document.querySelector('[data-testid="chart-host"]');
    const native = host?.querySelector(".tv-lightweight-charts");
    const table = native?.querySelector("table");
    return !!table?.rows.length && table.rows[table.rows.length - 1].getBoundingClientRect().height > 0;
  });
  const measureProduction = () => productionPage.evaluate(() => {
    const host = document.querySelector('[data-testid="chart-host"]');
    const panel = host.closest('[data-testid="panel-body"]');
    const native = host.querySelector(".tv-lightweight-charts");
    const tables = native.querySelectorAll("table");
    const axis = tables[tables.length - 1].rows[tables[tables.length - 1].rows.length - 1];
    const hostBox = host.getBoundingClientRect(), nativeBox = native.getBoundingClientRect();
    const axisBox = axis.getBoundingClientRect(), panelBox = panel.getBoundingClientRect();
    const line = host.querySelector("[data-entry-line]");
    return { nativeOffset: nativeBox.top - hostBox.top,
      axisClippedPx: Math.max(0, axisBox.bottom - hostBox.bottom),
      panelAxisClippedPx: Math.max(0, axisBox.bottom - panelBox.bottom),
      stopDrawnY: line?.getBoundingClientRect().top ?? null,
      markers: host.querySelectorAll("[data-order-group]").length };
  });
  const productionStates = [await measureProduction()];
  const hostBox = await productionPage.locator('[data-testid="chart-host"]').boundingBox();
  const cursor = { x: hostBox.x + hostBox.width * 0.55, y: hostBox.y + hostBox.height * 0.55 };
  await productionPage.mouse.move(cursor.x, cursor.y);
  await productionPage.keyboard.down("Shift");
  await productionPage.mouse.click(cursor.x, cursor.y); // No hover preview before the first click.
  await productionPage.keyboard.up("Shift");
  await productionPage.locator("[data-order-group]").waitFor({ state: "attached" });
  assert.equal(await productionPage.locator("[data-order-group]").count(), 1, "First Shift-click must submit exactly one order");
  productionStates.push(await measureProduction());
  await productionPage.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
  await productionPage.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  productionStates.push(await measureProduction());
  await productionPage.keyboard.down("Shift");
  await productionPage.mouse.move(cursor.x, cursor.y);
  await productionPage.waitForFunction(() => document.querySelector('[data-testid="chart-order-entry-preview"]')?.style.opacity === "1");
  const productionPreview = await measureProduction();
  assert(Math.abs(productionPreview.stopDrawnY - cursor.y) <= 8, "ChartPanel STOP preview must remain under the cursor");
  await productionPage.waitForTimeout(350);
  await productionPage.mouse.click(cursor.x, cursor.y);
  await productionPage.keyboard.up("Shift");
  await productionPage.locator("[data-order-group]").waitFor({ state: "attached" });
  productionStates.push(await measureProduction());
  await productionPage.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
  await productionPage.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  productionStates.push(await measureProduction());
  assert.equal(productionErrors.length, 0, productionErrors.join("\n"));
  assert.equal(await productionPage.evaluate(() => window.chartPanelProbe.errors.length), 0, "ChartPanel scheduler must not report paint errors");
  assert(productionStates.every(state => state.nativeOffset === 0), "The production ChartPanel must keep its native chart at the host origin");
  assert(productionStates.every(state => state.axisClippedPx === 0 && state.panelAxisClippedPx === 0), "The production ChartPanel must keep its full time axis inside the panel");

  console.log(`chart-order-layout passed: ${states.length} simulated overlay states and ${productionStates.length} PanelFrame/ChartPanel states`);
} finally {
  await browser?.close();
  await server.close();
}
