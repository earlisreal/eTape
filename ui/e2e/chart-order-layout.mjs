import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { createServer } from "vite";

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), "chart-order-layout");
const server = await createServer({ configFile: false, root, esbuild: { jsx: "automatic" }, server: {
  host: "127.0.0.1", port: 0, fs: { allow: [path.resolve(root, "../..")] },
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
  const measure = () => page.evaluate(() => window.repro.measure(280));
  const zero = await measure();

  await page.keyboard.down("Shift");
  await page.mouse.move(350, 280);
  await page.waitForFunction(() => document.querySelector('[data-testid="chart-order-entry-preview"]').style.opacity === "1");
  const firstPreview = await measure();
  await page.mouse.click(350, 280);
  await page.keyboard.up("Shift");
  await page.locator("[data-order-group]").waitFor({ state: "attached" });
  await page.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  const firstCanceled = await measure();

  await page.keyboard.down("Shift");
  await page.mouse.move(350, 280);
  await page.waitForFunction(() => document.querySelector('[data-testid="chart-order-entry-preview"]').style.opacity === "1");
  const secondPreview = await measure();
  await page.mouse.click(350, 280);
  await page.keyboard.up("Shift");
  await page.locator("[data-order-group]").waitFor({ state: "attached" });
  const secondOrder = await measure();
  await page.getByRole("button", { name: "Cancel BUY 1 STOP-LIMIT" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);
  const secondCanceled = await measure();

  const states = [zero, firstPreview, firstCanceled, secondPreview, secondOrder, secondCanceled];
  console.log(JSON.stringify({ states, errors }, null, 2));
  assert.equal(errors.length, 0, errors.join("\n"));
  assert(states.every(state => state.nativeOffset === 0), "Order changes must keep the native chart at the host origin");
  assert(states.every(state => state.axisClippedPx === 0), "Order changes must keep the full time axis visible");
  assert(states.every(state => state.panelAxisClippedPx === 0), "Order changes must keep the full time axis inside the panel body");
  assert(states.every(state => JSON.stringify(state.range) === JSON.stringify(zero.range)), "Order changes must preserve the time viewport");
  assert(states.every(state => Math.abs(state.candleY - zero.candleY) <= 1), "Order changes must preserve the price scale");
  assert.equal(firstPreview.actualStop, firstPreview.expectedStop, "First preview stop must match the price under the cursor");
  assert.equal(secondPreview.actualStop, secondPreview.expectedStop, "Second preview stop must match the price under the cursor");
  assert.equal(secondOrder.submittedStop, Number(secondPreview.previewStopText), "Submitted stop must match its displayed preview");
  assert(Math.abs(secondPreview.stopDrawnY - 280) <= 1, "Snapped stop line must remain under the cursor");
} finally {
  await browser?.close();
  await server.close();
}
