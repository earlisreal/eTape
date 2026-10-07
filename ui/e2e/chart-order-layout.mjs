import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { mkdir } from "node:fs/promises";
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
  const checkGesturePrice = async target => {
    const label = target.locator("[data-entry-preview-price]");
    assert(await label.isVisible(), "Holding the bound modifier must display the preview price");
    const price = await target.getByTestId("chart-order-entry-preview").getAttribute("data-price");
    assert.equal(await label.textContent(), Number(price).toFixed(Number(price) < 1 ? 4 : 2), "Preview label must show the snapped trigger price");
    const geometry = await label.evaluate(el => {
      const box = el.getBoundingClientRect(), host = el.closest('[data-testid="chart-host"]'), hostBox = host.getBoundingClientRect();
      return { top: box.top - hostBox.top, bottom: box.bottom - hostBox.top, right: box.right - hostBox.right,
        center: (box.top + box.bottom) / 2 - hostBox.top, y: Number(host.dataset.orderCursorY), color: getComputedStyle(el).color };
    });
    assert(geometry.top >= 0 && geometry.bottom <= (await target.getByTestId("chart-host").boundingBox()).height, "Preview price must stay inside the chart");
    assert(Math.abs(geometry.right) < 1 && Math.abs(geometry.center - geometry.y) < 1, "Preview price must align with its line on the right axis");
    assert.equal(geometry.color, "rgb(8, 153, 129)", "BUY preview price must use its side color");
  };
  const measureCrosshair = target => target.evaluate(async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const row = document.querySelector(".tv-lightweight-charts table").rows[0];
    const canvases = [...row.querySelectorAll("canvas")];
    const width = Math.max(...canvases.map(canvas => canvas.width));
    const canvas = canvases.findLast(canvas => canvas.width === width);
    const { data } = canvas.getContext("2d").getImageData(0, 0, canvas.width, canvas.height);
    const columns = new Array(canvas.width).fill(0);
    let maxRow = 0;
    for (let y = 0; y < canvas.height; y++) {
      let count = 0;
      for (let x = 0; x < canvas.width; x++) if (data[(y * canvas.width + x) * 4 + 3]) { count++; columns[x]++; }
      maxRow = Math.max(maxRow, count);
    }
    const maxColumn = Math.max(...columns), box = canvas.getBoundingClientRect();
    return { horizontal: maxRow > canvas.width / 3,
      verticalX: maxColumn > canvas.height / 5 ? box.left + columns.indexOf(maxColumn) * box.width / canvas.width : null };
  });
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
    assert.equal(await page.locator("[data-entry-line]").count(),1,"Modifier selection must show its own horizontal preview line");
    assert.equal(await page.locator("[data-entry-chip]").count(),0,"Modifier selection must have no preview price pill");
    await checkGesturePrice(page);
    assert((await page.getByTestId("chart-host").getAttribute("title")).includes("1 shares"),"Hover must expose order details");
    assert.equal(await page.getByTestId("chart-host").getAttribute("data-order-cursor-color"),"#089981");
    assert.equal(await page.evaluate(() => window.repro.crosshairOptions().horzLine.visible),false);
    assert.equal(await page.evaluate(() => window.repro.crosshairOptions().horzLine.labelVisible),false);
    assert.equal(await page.evaluate(() => window.repro.crosshairOptions().vertLine.color),"#787B86");
    if (cycle === 0) await page.waitForTimeout(350); // Let the 250ms route preview expire before clicking.
    await page.mouse.click(350, 280);
    assert.equal(await page.evaluate(() => window.repro.crosshairOptions().horzLine.visible),true,"Placement must restore the horizontal crosshair before modifier release");
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
      assert(preview.previewDetailText.includes("limit 5.12"), "Preview tooltip must show the cushion-adjusted LIMIT");
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
  const linePoint = await page.evaluate(() => window.repro.pointForPrice(4.7));
  const dragX = linePoint.x, dragY = linePoint.y;
  await page.mouse.move(dragX, dragY);
  assert.equal(await page.evaluate(({x,y}) => getComputedStyle(document.elementFromPoint(x,y)).cursor,{x:dragX,y:dragY}),"ns-resize","An editable line must expose a resize cursor");
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

  await page.evaluate(() => {
    window.repro.addOrder("same-buy-a", "STOP_LIMIT", 5.0);
    window.repro.addOrder("same-buy-b", "STOP_LIMIT", 5.0);
    window.repro.addOrder("same-sell", "STOP_LIMIT", 5.0, "SELL");
    window.repro.addOrder("nearby-sell", "LIMIT", 5.01, "SELL");
  });
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 3);
  const chips = await page.locator("[data-order-chip]").evaluateAll(nodes => nodes.map(node => {
    const box = node.getBoundingClientRect();
    return { top:box.top,bottom:box.bottom,right:box.right,color:getComputedStyle(node).color,text:node.innerText };
  }).sort((a,b) => a.top-b.top));
  assert(chips.every((chip,index) => !index || chip.top >= chips[index-1].bottom), "Same and neighboring price chips must remain individually clickable");
  assert(chips.some(chip => chip.text.includes("B 5.00 (2)") && chip.color === "rgb(8, 153, 129)"), "BUY chips must group only BUY orders and use chart green");
  assert(chips.some(chip => chip.text.includes("S 5.00") && chip.color === "rgb(242, 54, 69)"), "SELL chips must remain separate and use chart red");
  const axisHost = await page.getByTestId("chart-host").boundingBox();
  assert(chips.every(chip => Math.abs(chip.right - axisHost.x - axisHost.width) <= 1), "Working chips must sit on the right price axis");
  await page.getByTestId("order-label-same-buy-a").click();
  assert.equal(await page.getByRole("dialog",{name:"Choose chart order"}).count(),1,"Same-side count chip must retain its individual-order chooser");
  await page.getByRole("button",{name:"Close order chooser"}).click();
  await mkdir(path.resolve(root, "../.report"), { recursive: true });
  await page.screenshot({path:path.resolve(root,"../.report/chart-orders-compact.png")});
  await page.evaluate(() => ["same-buy-a","same-buy-b","same-sell","nearby-sell"].forEach(id => window.repro.finishOrder(id)));
  await page.waitForFunction(() => document.querySelectorAll("[data-order-group]").length === 0);

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
    return { nativeOffset: nativeBox.top - hostBox.top,
      axisClippedPx: Math.max(0, axisBox.bottom - hostBox.bottom),
      panelAxisClippedPx: Math.max(0, axisBox.bottom - panelBox.bottom),
      stopDrawnY: hostBox.top + Number(host.dataset.orderCursorY),
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
  await checkGesturePrice(productionPage);
  await productionPage.screenshot({ path: path.resolve(root, "../.report/chart-gesture-price-preview.png") });
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

  const measureRisk = () => productionPage.evaluate(() => {
    const host = document.querySelector('[data-testid="chart-host"]');
    const pane = host.querySelector(".tv-lightweight-charts table").rows[0];
    const bar = host.querySelector("[data-risk-bar]");
    const box = bar.getBoundingClientRect(), paneBox = pane.getBoundingClientRect(), style = getComputedStyle(bar);
    const readout = host.querySelector("[data-risk-readout]"), readoutBox = readout.getBoundingClientRect(), readoutStyle = getComputedStyle(readout);
    const arrow = host.querySelector("[data-risk-arrow]"), arrowPath = arrow.querySelector("path");
    const label = host.querySelector("[data-risk-preview-price]"), labelBox = label.getBoundingClientRect();
    const coordinates = (arrowPath.getAttribute("d") ?? "").split(" ");
    return { top: box.top, bottom: box.bottom, left: box.left, right: box.right, height: box.height,
      paneTop: paneBox.top, paneBottom: paneBox.bottom, hostBottom: host.getBoundingClientRect().bottom,hostRight:host.getBoundingClientRect().right,
      gutterLeft: pane.cells[pane.cells.length - 1].getBoundingClientRect().left,
      background: style.backgroundColor, border: style.borderTopWidth, pointerEvents: style.pointerEvents, color: style.color,
      buttons: bar.querySelectorAll("button").length, fitsText: bar.scrollWidth <= bar.clientWidth, text: bar.innerText,
      readout:{visible:readoutStyle.display !== "none",text:readout.innerText,left:readoutBox.left,right:readoutBox.right,top:readoutBox.top,bottom:readoutBox.bottom,color:readoutStyle.color,
        background:readoutStyle.backgroundColor,border:readoutStyle.borderTopWidth,pointerEvents:readoutStyle.pointerEvents},
      arrow:{visible:getComputedStyle(arrow).display !== "none",x:Number(coordinates[1]),fromY:Number(coordinates[2]),toY:Number(coordinates[4]),fill:arrowPath.getAttribute("fill"),rectangles:arrow.querySelectorAll("rect").length},
      lineYs:["buy","sell"].map(side=>parseFloat(host.querySelector(`[data-risk-${side}]`).style.top)),
      label:{visible:getComputedStyle(label).display !== "none",text:label.textContent,color:getComputedStyle(label).color,
        top:labelBox.top,bottom:labelBox.bottom,right:labelBox.right,buttons:label.querySelectorAll("button").length},
      chips:[...host.querySelectorAll("[data-risk-chip]")].filter(chip => getComputedStyle(chip).display !== "none").map(chip => {
        const box = chip.getBoundingClientRect(), button = chip.querySelector("[data-risk-price]");
        return {top:box.top,bottom:box.bottom,right:box.right,text:button.innerText,title:button.title,color:getComputedStyle(chip).color};
      }) };
  });
  const checkRisk = async () => {
    const risk = await measureRisk();
    assert(Math.abs(risk.bottom - (risk.paneBottom - 6)) <= 2, "Risk bar must float at the main pane bottom");
    assert(risk.top >= risk.paneTop && risk.right < risk.gutterLeft, "Risk bar must stay inside the plot and clear its price gutter");
    assert.equal(risk.background, "rgba(0, 0, 0, 0)", "Risk bar must be transparent");
    assert.equal(risk.border, "0px", "Risk bar must have no border");
    assert.equal(risk.pointerEvents, "none", "Risk bar must let chart pointer input pass through");
    assert.equal(risk.buttons, 0, "Routine instructions must not add a separate action bar");
    assert(risk.fitsText, "Risk text must wrap within a narrow chart");
    assert(!risk.text.includes("notional") && !risk.text.includes("Est. risk"), "Routine bottom instructions must stay compact");
    if (risk.readout.visible) {
      assert(risk.arrow.visible && risk.arrow.rectangles === 0 && risk.arrow.fill === "none", "Risk preview must use an unfilled arrow without a rectangle");
      assert(risk.readout.text.includes("shares · Est. risk"), "Shares and estimated risk must be visible beside the arrow");
      assert(risk.readout.left >= hostBox.x && risk.readout.right < risk.gutterLeft && risk.readout.top >= risk.paneTop && risk.readout.bottom <= risk.paneBottom,
        "Risk readout must stay within the main price pane");
      assert(risk.readout.background === "rgba(0, 0, 0, 0)" && risk.readout.border === "0px" && risk.readout.pointerEvents === "none", "Risk readout must remain transparent and pass pointer input through");
      assert(Math.abs(risk.arrow.fromY - risk.lineYs[0]) <= 1, "Risk arrow must begin at the actual BUY trigger");
      if (risk.chips.length === 2) assert(Math.abs(risk.arrow.toY - risk.lineYs[1]) <= 1, "Risk arrow must end at the actual SELL trigger, independently of spaced chips");
    }
    assert(risk.chips.every(chip => Math.abs(chip.right - risk.hostRight) <= 1), "Risk chips must align with the price axis");
    if (risk.label.visible) {
      assert(Math.abs(risk.label.right - risk.hostRight) <= 1 && risk.label.top >= risk.paneTop && risk.label.bottom <= risk.paneBottom,
        "The plain preview price must stay on the axis inside the main pane");
      assert(risk.chips.every(chip => risk.label.bottom <= chip.top || risk.label.top >= chip.bottom), "Preview prices and placed pills must not overlap");
    }
    const geometry = await measureProduction();
    assert(geometry.nativeOffset === 0 && geometry.axisClippedPx === 0 && geometry.panelAxisClippedPx === 0,
      "Risk setup must preserve the native chart origin and full time axis");
    return risk;
  };
  await mkdir(path.resolve(root, "../.report"), { recursive: true });
  for (const theme of ["light", "dark"]) {
    await productionPage.evaluate(theme => window.chartPanelProbe.setTheme(theme), theme);
    await productionPage.waitForFunction(theme => document.documentElement.dataset.theme === theme, theme);
    await productionPage.mouse.move(cursor.x, hostBox.y + hostBox.height * 0.30);
    assert(await productionPage.evaluate(() => window.chartPanelProbe.startRisk()), "Risk setup must start in the active chart");
    await productionPage.getByTestId("chart-risk-entry").waitFor({ state: "visible" });
    const setup = await checkRisk();
    assert(setup.height <= 28, "Wide-chart setup should use two compact rows");
    assert.equal(setup.color, theme === "light" ? "rgb(19, 23, 34)" : "rgb(209, 212, 220)", "Risk text must follow the chart theme");
    assert(setup.label.visible && setup.label.buttons === 0 && setup.chips.length === 0,"Initial BUY must have a plain price label and no placed pill");
    assert.equal(setup.label.color,"rgb(8, 153, 129)");
    if (theme === "dark") await productionPage.mouse.down();
    else await productionPage.mouse.click(cursor.x, hostBox.y + hostBox.height * 0.30);
    await productionPage.mouse.move(cursor.x + 40, hostBox.y + hostBox.height * 0.50);
    await productionPage.waitForFunction(() => document.querySelector("[data-risk-readout]").innerText.includes("shares · Est. risk $"));
    const moving = await checkRisk();
    assert.equal(moving.chips.length,1,"Live risk sizing must appear before selecting SELL");
    assert(moving.label.visible && moving.label.buttons === 0 && moving.label.color === "rgb(242, 54, 69)","SELL must preview a plain red price while retaining the selected BUY pill");
    assert(Math.abs(moving.lineYs[1] - hostBox.height * 0.50) <= 3,"The moving SELL line must follow its snapped trigger during both placement gestures");
    const crosshair = await measureCrosshair(productionPage);
    assert(!crosshair.horizontal,"Risk selection must hide the native horizontal crosshair");
    assert(Math.abs(crosshair.verticalX - cursor.x - 40) <= 2,"The native vertical crosshair must follow captured risk pointer moves");
    if (theme === "dark") await productionPage.screenshot({path:path.resolve(root,"../.report/chart-risk-entry-drag-preview.png")});
    assert(Math.abs(moving.arrow.x - (cursor.x - hostBox.x + 20)) <= 1,"Risk arrow must be centered between the BUY and candidate SELL X positions");
    await productionPage.mouse.move(cursor.x + 40, hostBox.y + hostBox.height * 0.60);
    await productionPage.waitForFunction(previous => document.querySelector("[data-risk-readout]").innerText !== previous,moving.readout.text);
    await productionPage.mouse.move(hostBox.x + hostBox.width - 5, hostBox.y + hostBox.height * 0.60);
    await productionPage.waitForFunction(() => document.querySelector("[data-risk-readout]").style.display === "none");
    assert.equal((await measureRisk()).label.visible,false,"The moving price label must hide outside the price pane");
    if (theme === "dark") {
      await productionPage.mouse.move(cursor.x + 40, hostBox.y + hostBox.height * 0.50);
      await productionPage.mouse.up();
    } else await productionPage.mouse.click(cursor.x + 40, hostBox.y + hostBox.height * 0.50);
    await productionPage.waitForFunction(() => document.querySelector("[data-risk-price='buy']").title.includes("planned shares"));
    const preview = await checkRisk();
    assert.equal(preview.label.visible,false,"Placed risk prices must use their pills instead of the plain preview label");
    assert((await measureCrosshair(productionPage)).horizontal,"Completing the risk pair must restore the native horizontal crosshair");
    assert(preview.readout.visible && preview.arrow.toY > preview.arrow.fromY,"Completed draft must retain its downward protective-SELL arrow and readout");
    assert.equal(preview.readout.color,setup.color,"Live risk text must follow the chart theme");
    assert(preview.chips.length === 2 && preview.chips[0].title.includes("BUY STOP-LIMIT") && preview.chips[1].title.includes("SELL STOP-LIMIT"), "Both compact risk chips must expose order details on hover");
    assert(preview.chips.every(chip => chip.title.includes("planned shares") && chip.title.includes("Held by eTape") && chip.title.includes("fees/execution risk excluded") && !chip.title.includes("est. risk")), "Tooltips must retain shares and custody without risk amounts");
    assert.deepEqual(preview.chips.map(chip => chip.color),["rgb(8, 153, 129)","rgb(242, 54, 69)"],"Risk draft colors must match submitted BUY/SELL chips");
    await productionPage.screenshot({ path: path.resolve(root, `../.report/chart-risk-entry-${theme}.png`) });
    await productionPage.keyboard.press("Enter");
    await productionPage.getByTestId("chart-risk-entry").waitFor({ state: "hidden" });
    assert((await productionPage.evaluate(() => window.chartPanelProbe.lastRiskSubmitted)).maxQty > 0, "Enter must submit the simulated pair");
  }
  await productionPage.getByRole("button", { name: "indicators", exact: true }).click();
  await productionPage.getByRole("button", { name: "add MACD", exact: true }).click();
  await productionPage.evaluate(() => window.chartPanelProbe.startRisk());
  await productionPage.getByTestId("chart-risk-entry").waitFor({ state: "visible" });
  await productionPage.evaluate(() => {
    const shell = document.querySelector('[data-testid="panel-shell"]');
    shell.style.width = "320px";
    shell.style.height = "640px";
  });
  await productionPage.waitForFunction(() => document.querySelector('[data-testid="chart-host"]').clientWidth === 320);
  await productionPage.waitForTimeout(1100); // Risk layout also follows asynchronously resized indicator panes.
  await productionPage.waitForFunction(() => {
    const bar = document.querySelector("[data-risk-bar]").getBoundingClientRect();
    const pane = document.querySelector(".tv-lightweight-charts table").rows[0].getBoundingClientRect();
    return Math.abs(bar.bottom - (pane.bottom - 6)) <= 2;
  });
  const narrow = await checkRisk();
  assert(narrow.hostBottom - narrow.paneBottom > 100, "The narrow scenario must include a lower indicator pane");
  const narrowHost = await productionPage.getByTestId("chart-host").boundingBox();
  await productionPage.mouse.click(narrowHost.x + 100,narrow.paneTop + 100);
  await productionPage.mouse.click(narrowHost.x + 100,narrow.paneTop + 115);
  await productionPage.waitForFunction(() => document.querySelector("[data-risk-price='buy']").title.includes("planned shares"));
  const narrowChips = (await measureRisk()).chips.sort((a,b) => a.top-b.top);
  await checkRisk();
  assert(narrowChips[1].top >= narrowChips[0].bottom,"Nearby draft prices must retain separate axis controls");
  const draftBuy = await productionPage.locator("[data-risk-price='buy']").boundingBox();
  const originalBuy = await productionPage.locator("[data-risk-price='buy']").textContent();
  await productionPage.mouse.move(draftBuy.x + draftBuy.width/2,draftBuy.y + draftBuy.height/2);
  await productionPage.mouse.down();
  await productionPage.mouse.move(draftBuy.x + draftBuy.width/2,draftBuy.y + draftBuy.height/2 - 10);
  await productionPage.mouse.up();
  assert.notEqual(await productionPage.locator("[data-risk-price='buy']").textContent(),originalBuy,"Risk axis handles must remain draggable when spaced apart");
  await productionPage.screenshot({ path: path.resolve(root, "../.report/chart-risk-entry-narrow.png") });
  await productionPage.getByRole("button",{name:"Discard risk setup"}).first().click();
  await productionPage.getByTestId("chart-risk-entry").waitFor({ state: "hidden" });
  assert.equal(productionErrors.length, 0, productionErrors.join("\n"));
  assert.equal(await productionPage.evaluate(() => window.chartPanelProbe.errors.length), 0, "Risk setup must not report chart paint errors");

  console.log(`chart-order-layout passed: ${states.length} simulated overlay states, ${productionStates.length} PanelFrame/ChartPanel states, light/dark risk preview and narrow multi-pane risk setup`);
} finally {
  await browser?.close();
  await server.close();
}
