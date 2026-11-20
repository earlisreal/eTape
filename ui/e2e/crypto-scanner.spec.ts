import { expect, test, type WebSocketRoute } from "@playwright/test";
import { buildCryptoMonitoringWorkspace } from "../src/chrome/presets";
import type { ClientMessage, ExecStatus } from "../src/wire/contract";
import { blankWorkspace } from "../src/chrome/workspace";

function preserveSavedLayout(server: WebSocketRoute, socket: WebSocketRoute, accounts: Pick<ExecStatus["venues"][number], "venue" | "broker" | "env">[] = []) {
  // Keep demo startup from rewriting the workspace or adding a Watchlist tab.
  server.onMessage(raw => {
    const message = JSON.parse(raw.toString()) as { kind: string; topic?: string; payload?: object };
    if (message.kind === "snapshot" && message.topic === "sys.session") {
      socket.send(JSON.stringify({ ...message, payload: { ...message.payload, mode: "live" } }));
    } else if (message.topic === "exec.status" && accounts.length) {
      const status = message.payload as ExecStatus;
      const template = status.venues.find(venue => venue.venue === "sim-paper");
      socket.send(template ? JSON.stringify({ ...message, payload: {
        ...status, venues: [...status.venues, ...accounts.map(account => ({ ...template, ...account }))],
      } }) : raw);
    } else socket.send(raw);
  });
}

test.beforeEach(async ({ context }) => {
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
});

test("Crypto Scanner keeps its saved layout and fits its controls in a narrow panel", async ({ page }, testInfo) => {
  let workspace = buildCryptoMonitoringWorkspace();
  const scanner = workspace.panels.find(panel => panel.panelId === "crypto-scanner")!;
  scanner.settings = { ...scanner.settings, cryptoExchange: "BINANCE-DEMO", cryptoMode: "Most Active", minTurnover: 1_000_000 };
  const workspaceKey = `workspace.${workspace.name}`;
  await page.routeWebSocket("**/ws", socket => {
    const server = socket.connectToServer();
    preserveSavedLayout(server, socket);
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

test("Account switches a linked Crypto Scanner across windows and restores the last contracts", async ({ page, context }) => {
  const trading = blankWorkspace("e2e-account-market");
  trading.groups = { green: "US.AAPL" };
  trading.linkVenues = { green: "sim-paper" }; // legacy stock account selection
  trading.panels = [
    { id: "account", panelId: "account", group: "green", settings: { ordersHeight: 160 } },
    { id: "chart", panelId: "chart", group: "green", settings: { timeframe: "1m", symbol: "US.MSFT" } },
  ];
  const scanning = blankWorkspace("e2e-follow-account");
  scanning.groups = { green: "US.AAPL" };
  scanning.panels = [
    { id: "linked", panelId: "crypto-scanner", group: "green", settings: { cryptoMode: "Most Active" } },
    { id: "fixed", panelId: "crypto-scanner", group: null, settings: { cryptoExchange: "MEXC", cryptoMode: "Most Active" } },
  ];
  const docs = new Map([[`workspace.${trading.name}`, trading], [`workspace.${scanning.name}`, scanning]]);
  await context.routeWebSocket("**/ws", socket => {
    const server = socket.connectToServer();
    preserveSavedLayout(server, socket, [
      { venue: "mexc-paper", broker: "mexc-sim", env: "paper" },
      { venue: "mexc-live", broker: "mexc", env: "live" },
      { venue: "bingx-paper", broker: "bingx-vst", env: "paper" },
      { venue: "bingx-live", broker: "bingx-live", env: "live" },
      { venue: "binance-demo", broker: "binance-demo", env: "paper" },
    ]);
    socket.onMessage(raw => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind !== "command") { server.send(raw); return; }
      const args = message.args as { key?: string; value?: typeof trading; exchange?: string };
      const ack = (value?: unknown) => socket.send(JSON.stringify({ kind: "ack", status: "accepted", corrId: message.corrId, value }));
      if (args.key && docs.has(args.key) && message.name === "GetConfig") {
        ack(docs.get(args.key));
      } else if (args.key && docs.has(args.key) && message.name === "SetConfig" && args.value) {
        docs.set(args.key, args.value);
        ack();
      } else if (message.name === "GetCryptoScanner") {
        ack({ asOfMs: Date.now(), warming: false, rows: [
          { symbol: `PERP.${args.exchange}.BTCUSDT`, base: "BTC", last: 62100, changePct: 4.2, turnover: 3900000, spreadBps: 0.8 },
        ] });
      } else server.send(raw);
    });
  });
  const scannerWindow = await context.newPage();
  await Promise.all([
    page.goto(`/?workspace=${trading.name}`),
    scannerWindow.goto(`/?workspace=${scanning.name}`),
  ]);
  const symbol = page.getByTestId("panel-symbol");
  const accountHeader = page.getByTestId("panel-header-actions").filter({ has: page.getByRole("group", { name: "Account market" }) });
  const venue = accountHeader.getByRole("combobox", { name: "Execution Venue" });
  const stocks = accountHeader.getByRole("button", { name: "Stocks", exact: true });
  const crypto = accountHeader.getByRole("button", { name: "Crypto", exact: true });
  const fixed = scannerWindow.getByRole("table", { name: "MEXC crypto Scanner results" });
  const demo = scannerWindow.getByRole("table", { name: "Binance Futures Demo crypto Scanner results" });
  const paused = scannerWindow.getByText("Paused while Green is on Stocks");

  await expect(venue).toHaveValue("sim-paper");
  await expect(symbol).toHaveText("AAPL");
  await scannerWindow.getByTestId("scanner-header-controls").first().getByRole("button", { name: "filters" }).click();
  await scannerWindow.getByLabel("Crypto Scanner exchange").selectOption("follow-account");
  await scannerWindow.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(paused).toBeVisible();
  await expect(fixed.getByRole("button", { name: "BTC/USDT" })).toBeVisible();

  await crypto.click();
  await expect(page.getByRole("combobox", { name: "Account exchange" })).toHaveCount(0);
  await expect(venue.locator("option")).toHaveText([
    "Choose a crypto account", "mexc-paper", "mexc-live", "bingx-paper", "bingx-live", "binance-demo",
  ]);
  await expect(symbol).toHaveText("—");
  await venue.selectOption("mexc-paper");
  await expect(fixed).toHaveCount(2);
  page.once("dialog", dialog => dialog.accept());
  await venue.selectOption("mexc-live");
  await expect(venue).toHaveValue("mexc-live");
  await expect(fixed).toHaveCount(2);
  await venue.selectOption("bingx-paper");
  const bingx = scannerWindow.getByRole("table", { name: "BingX crypto Scanner results" });
  await expect(bingx).toBeVisible();
  page.once("dialog", dialog => dialog.accept());
  await venue.selectOption("bingx-live");
  await expect(venue).toHaveValue("bingx-live");
  await expect(bingx).toBeVisible();
  await venue.selectOption("binance-demo");
  await expect(symbol).toHaveText("—");
  const btc = demo.getByRole("button", { name: "BTC/USDT" });
  await expect(btc).toBeVisible();
  await btc.dblclick();
  await expect.poll(() => docs.get(`workspace.${trading.name}`)?.groups?.green).toBe("PERP.BINANCE-DEMO.BTCUSDT");

  await stocks.click();
  await expect(paused).toBeVisible();
  await expect(demo).toHaveCount(0);
  await expect(fixed.getByRole("button", { name: "BTC/USDT" })).toBeVisible();
  await expect(symbol).toHaveText("AAPL");
  await expect(venue).toHaveValue("sim-paper");
  await expect.poll(() => docs.get(`workspace.${trading.name}`)?.groups?.green).toBe("US.AAPL");
  await page.reload();
  await crypto.click();
  await expect(venue).toHaveValue("binance-demo");
  await expect(btc).toBeVisible();
  await expect.poll(() => docs.get(`workspace.${trading.name}`)?.groups?.green).toBe("PERP.BINANCE-DEMO.BTCUSDT");
});
