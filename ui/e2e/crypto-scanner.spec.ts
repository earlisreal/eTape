import { expect, test } from "@playwright/test";
import { buildCryptoMonitoringWorkspace } from "../src/chrome/presets";
import type { ClientMessage } from "../src/wire/contract";

test("Crypto Scanner keeps its saved layout and fits its controls in a narrow panel", async ({ page }, testInfo) => {
  let workspace = buildCryptoMonitoringWorkspace();
  const scanner = workspace.panels.find(panel => panel.panelId === "crypto-scanner")!;
  scanner.settings = { ...scanner.settings, cryptoExchange: "BINANCE-DEMO", cryptoMode: "Most Active", minTurnover: 1_000_000 };
  const workspaceKey = `workspace.${workspace.name}`;
  await page.routeWebSocket("**/ws", socket => {
    const server = socket.connectToServer();
    socket.onMessage(raw => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind !== "command") { server.send(raw); return; }
      const args = message.args as { key?: string; value?: typeof workspace; exchange?: string };
      const ack = (value?: unknown) => socket.send(JSON.stringify({ kind: "ack", status: "accepted", corrId: message.corrId, value }));
      if (message.name === "GetConfig" && args?.key === workspaceKey) {
        ack(workspace);
      } else if (message.name === "SetConfig" && args?.key === workspaceKey && args.value) {
        workspace = args.value;
        ack();
      } else if (message.name === "GetCryptoScanner") {
        ack({ asOfMs: Date.now(), warming: false, rows: [
          { symbol: `PERP.${args.exchange}.BTCUSDT`, base: "BTC", last: 62100, changePct: 4.2, turnover: 3900000, spreadBps: 0.8 },
          { symbol: `PERP.${args.exchange}.ETHUSDT`, base: "ETH", last: 2100, changePct: -2.4, turnover: 2000000, spreadBps: 1.5 },
        ] });
      } else server.send(raw);
    });
  });
  await page.setViewportSize({ width: 1000, height: 800 });
  await page.goto(`/?workspace=${workspace.name}`);
  const header = page.getByTestId("scanner-header-controls");
  const gear = header.getByRole("button", { name: "filters" });
  const results = page.getByRole("table", { name: "Binance Futures Demo crypto Scanner results" });
  const btc = results.getByRole("row", { name: /BTC\/USDT/ });
  const eth = results.getByRole("row", { name: /ETH\/USDT/ });
  await expect(page.getByTestId("chart-host")).toHaveCount(4);
  await expect(eth).toContainText("−2.40%");
  await expect(header.getByRole("button", { name: "Mute Scanner sounds" })).toBeVisible();
  await expect(results.getByRole("columnheader", { name: "24-hour turnover (USDT)" })).toHaveAttribute("aria-sort", "descending");

  await btc.click();
  await expect(btc).toHaveCSS("box-shadow", /1px inset/);
  const spread = results.getByRole("columnheader", { name: "Spread (bps)" });
  await spread.getByRole("button").click();
  await expect(spread).toHaveAttribute("aria-sort", "descending");
  await expect(results.locator("tbody tr").first()).toContainText("ETH/USDT");

  await gear.click();
  const exchange = page.getByLabel("Crypto Scanner exchange");
  const minTurnover = page.getByLabel("Min 24h turnover (USDT)");
  await expect(exchange).toHaveValue("BINANCE-DEMO");
  await expect(minTurnover).toHaveValue("1000000");
  await minTurnover.fill("3000000");
  await expect(eth).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(gear).toBeFocused();
  await expect(exchange).toHaveCount(0);
  await gear.click();
  await expect(minTurnover).toHaveValue("1000000");
  await minTurnover.fill("3000000");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(eth).toHaveCount(0);
  await expect.poll(() => workspace.panels.find(panel => panel.id === scanner.id)?.settings.minTurnover).toBe(3000000);

  await page.reload();
  await expect(page.getByTestId("chart-host")).toHaveCount(4);
  await expect(btc).toBeVisible();
  await expect(eth).toHaveCount(0);
  await expect(spread).toHaveAttribute("aria-sort", "descending");
  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("radio", { name: theme, exact: true }).check();
    await page.keyboard.press("Escape");
    await expect(btc.getByRole("cell", { name: "+4.20%" })).toHaveCSS("color", await page.locator("html").evaluate(
      root => getComputedStyle(root).getPropertyValue("--up").trim().replace(/^#(\w\w)(\w\w)(\w\w)$/, (_, r, g, b) =>
        `rgb(${[r, g, b].map(channel => parseInt(channel, 16)).join(", ")})`),
    ));
    await gear.click();
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath(`${theme.toLowerCase()}-crypto-scanner.png`) });
    await page.keyboard.press("Escape");
  }
});
