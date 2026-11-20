import { expect, test, type WebSocketRoute } from "@playwright/test";
import { buildCryptoMonitoringWorkspace } from "../src/chrome/presets";
import type { PerpetualMarket } from "../src/gen/wsmsg";
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

function market(exchange: string, base: string): PerpetualMarket {
  const separator = exchange === "MEXC" ? "_" : exchange === "BINGX" ? "-" : "";
  return { symbol: `PERP.${exchange}.${base}${separator}USDT`, exchange, base, settlement: "USDT", tradingEnabled: false };
}

test.beforeEach(async ({ context }) => {
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
});

test("Crypto List restores legacy settings and fits its controls in a narrow panel", async ({ page }, testInfo) => {
  let workspace = buildCryptoMonitoringWorkspace();
  const scanner = workspace.panels.find(panel => panel.panelId === "crypto-scanner")!;
  scanner.settings = { ...scanner.settings, cryptoExchange: "BINANCE-DEMO", cryptoMode: "5m Losers", minTurnover: 99_000_000, cryptoSort: { col: "spreadBps", dir: "asc" } };
  (workspace.layout as { panels: Record<string, { title: string }> }).panels[scanner.id].title = "Crypto Scanner";
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
      } else if (args.exchange && message.name === "ListPerpetualMarkets") {
        const exchange = args.exchange;
        ack(["BTC", "ETH", "SOL"].map(base => market(exchange, base)));
      } else if (args.exchange && message.name === "GetCryptoScanner") {
        ack({ asOfMs: Date.now(), rows: [
          { symbol: market(args.exchange, "BTC").symbol, last: 62100, reportedChangePct: 4.2, turnover: 3900000 },
          { symbol: market(args.exchange, "ETH").symbol, last: 2100, reportedChangePct: -2.4, turnover: 2000000 },
        ] });
      } else server.send(raw);
    });
  });
  await page.setViewportSize({ width: 1000, height: 800 });
  await page.goto(`/?workspace=${workspace.name}`);
  const results = page.getByRole("table", { name: "Binance Futures Demo Crypto List" });
  const btc = results.getByRole("row", { name: /BTC\/USDT/ });
  const eth = results.getByRole("row", { name: /ETH\/USDT/ });
  const sol = results.getByRole("row", { name: /SOL\/USDT/ });
  const exchange = page.getByLabel("Crypto List exchange");
  const search = page.getByRole("searchbox", { name: "Search USDT perpetuals" });
  await expect(page.getByTestId("chart-host")).toHaveCount(4);
  await expect(page.getByText("Crypto List", { exact: true })).toBeVisible();
  await expect(eth).toContainText("−2.40%");
  await expect(sol.getByRole("cell", { name: "—" })).toHaveCount(3);
  await expect(results.getByRole("columnheader", { name: "24h Turnover (USDT)" })).toHaveAttribute("aria-sort", "descending");
  await expect(exchange).toHaveValue("BINANCE-DEMO");

  await btc.click();
  await expect(btc).toHaveCSS("box-shadow", /1px inset/);
  const change = results.getByRole("columnheader", { name: "Change %" });
  await change.getByRole("button").click();
  await change.getByRole("button").click();
  await expect(change).toHaveAttribute("aria-sort", "ascending");
  await expect(results.locator("tbody tr").first()).toContainText("ETH/USDT");
  await expect(results.locator("tbody tr").last()).toContainText("SOL/USDT");

  await search.fill("btc/usdt");
  await expect(btc).toBeVisible();
  await expect(eth).toHaveCount(0);
  await expect.poll(() => workspace.panels.find(panel => panel.id === scanner.id)?.settings.cryptoSort)
    .toEqual({ col: "reportedChangePct", dir: "asc" });

  await page.reload();
  await expect(page.getByTestId("chart-host")).toHaveCount(4);
  await expect(btc).toBeVisible();
  await expect(eth).toBeVisible();
  await expect(change).toHaveAttribute("aria-sort", "ascending");
  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("radio", { name: theme, exact: true }).check();
    await page.keyboard.press("Escape");
    await expect(search).toBeInViewport();
    await expect(exchange).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath(`${theme.toLowerCase()}-crypto-list.png`) });
  }
});

test("Account switches a linked Crypto List across windows and restores the last contracts", async ({ page, context }) => {
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
      } else if (args.exchange && message.name === "ListPerpetualMarkets") {
        ack([market(args.exchange, "BTC"), market(args.exchange, "ETH")]);
      } else if (args.exchange && message.name === "GetCryptoScanner") {
        ack({ asOfMs: Date.now(), rows: [
          { symbol: market(args.exchange, "BTC").symbol, last: 62100, reportedChangePct: 4.2, turnover: 3900000 },
        ] });
      } else if (message.name === "FocusGroup" && (args as { symbol?: string }).symbol?.startsWith("PERP.")) {
        ack();
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
  const fixed = scannerWindow.getByRole("table", { name: "MEXC Crypto List" });
  const demo = scannerWindow.getByRole("table", { name: "Binance Futures Demo Crypto List" });
  const paused = scannerWindow.getByText("Paused while the Link Group is on Stocks");

  await expect(venue).toHaveValue("sim-paper");
  await expect(symbol).toHaveText("AAPL");
  await scannerWindow.getByLabel("Crypto List exchange").first().selectOption("follow-account");
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
  const bingx = scannerWindow.getByRole("table", { name: "BingX Crypto List" });
  await expect(bingx).toBeVisible();
  page.once("dialog", dialog => dialog.accept());
  await venue.selectOption("bingx-live");
  await expect(venue).toHaveValue("bingx-live");
  await expect(bingx).toBeVisible();
  await venue.selectOption("binance-demo");
  await expect(symbol).toHaveText("—");
  const btc = demo.getByRole("button", { name: "BTC/USDT" });
  await expect(btc).toBeVisible();
  await expect(page.getByRole("button", { name: "Browse USDT perpetuals" })).toHaveCount(0);
  const search = scannerWindow.getByRole("searchbox", { name: "Search USDT perpetuals" }).first();
  await search.fill("BTC");
  await expect(demo.getByText("ETH/USDT")).toHaveCount(0);
  await search.press("Enter");
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
