import { test, expect } from "@playwright/test";
import type { ClientMessage, ExecStatus } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION, type Workspace } from "../src/chrome/workspace";

function venueWorkspace(workspace: string): Workspace {
  return {
    name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION,
    panels: [
      { id: "ticket", panelId: "order-ticket", group: "blue", settings: {} },
      { id: "account", panelId: "account", group: "blue", settings: {} },
    ],
    groups: { blue: "US.DRGO" }, linkVenues: { blue: "sim-paper" },
    layout: {
      grid: { root: { type: "branch", data: [
        { type: "leaf", data: { id: "ticket", views: ["ticket"], activeView: "ticket" }, size: 640 },
        { type: "leaf", data: { id: "account", views: ["account"], activeView: "account" }, size: 640 },
      ], size: 800 }, width: 1280, height: 800, orientation: "HORIZONTAL" },
      panels: {
        ticket: { id: "ticket", contentComponent: "ticket", title: "order-ticket" },
        account: { id: "account", contentComponent: "account", title: "account" },
      }, activeGroup: "ticket",
    },
  };
}

test("venue badges remain visible across themes, narrow headers, and workspace targets", async ({ page, context }, testInfo) => {
  let environment = "live";
  const publishers: Array<() => void> = [];
  const longVenue = "a-very-long-venue-name-paper";
  const workspace = `e2e-venue-badge-${Date.now()}`;
  const seed = venueWorkspace(workspace);
  // Only display metadata is changed. The isolated demo engine remains sim-only.
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: seed }));
      } else server.send(raw);
    });
    server.onMessage((message) => {
      if (typeof message !== "string") { socket.send(message); return; }
      const frame = JSON.parse(message);
      if (frame.topic !== "exec.status") { socket.send(message); return; }
      const status: ExecStatus = frame.payload;
      const publish = () => socket.send(JSON.stringify({ ...frame, payload: {
        ...status, masterArmed: false, venues: [
          ...status.venues.map((v) => ({ ...v, env: environment, connected: false })),
          { ...status.venues[0], venue: longVenue, env: "live", connected: false },
        ],
      } }));
      publishers.push(publish);
      publish();
    });
  });
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("acct-equity")).toBeVisible();
  await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
  const ticket = page.getByTestId("panel-tab-ticket").locator(".ledger-header");
  const account = page.getByTestId("panel-tab-account").locator(".ledger-header");
  await page.getByTestId("venue").selectOption("sim-paper");
  await ticket.click();
  const cue = page.getByTestId("hotkey-target-cue");
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await expect(ticket.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await expect(account.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await page.getByTestId("venue").selectOption(longVenue);
  const other = await context.newPage();
  await other.goto(`/?workspace=e2e-venue-badge-${Date.now()}`);
  await expect(other.getByTestId("hotkey-target-cue").getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await page.bringToFront();

  for (const theme of ["Light", "Dark"]) {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("radio", { name: theme, exact: true }).check();
    await page.mouse.click(5, 5);
    for (const width of [1280, 900]) {
      await page.setViewportSize({ width, height: 900 });
      for (const host of [page.getByTestId("top-bar"), ticket, account]) {
        const badge = host.getByTestId("venue-environment-badge");
        await expect(badge).toBeVisible();
        const bounds = await badge.evaluate((el) => {
          const rect = el.getBoundingClientRect();
          const host = el.closest(".ledger-header, .top-bar")!.getBoundingClientRect();
          return { left: rect.left, right: rect.right, hostLeft: host.left, hostRight: host.right, width: rect.width,
            covered: !el.contains(document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)) };
        });
        expect(bounds.width).toBeGreaterThan(20);
        expect(bounds.left).toBeGreaterThanOrEqual(bounds.hostLeft);
        expect(bounds.right).toBeLessThanOrEqual(bounds.hostRight);
        expect(bounds.covered).toBe(false);
      }
      await page.screenshot({ path: testInfo.outputPath(`${theme.toLowerCase()}-${width}.png`) });
    }
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByTestId("venue").selectOption("sim-paper");
  environment = "paper";
  publishers.forEach((publish) => publish());
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("SIM");
  await expect(other.getByTestId("hotkey-target-cue").getByTestId("venue-environment-badge")).toHaveText("SIM");
  await other.close();
});

test("linked panels recall the stock and exchange accounts", async ({ page, context }) => {
  const workspace = "e2e-venue-market";
  const seed = venueWorkspace(workspace);
  await context.routeWebSocket("**/ws", socket => {
    const server = socket.connectToServer();
    socket.onMessage(raw => {
      const message = JSON.parse(raw.toString());
      if (message.name === "GetConfig" && message.args.key === "workspace." + workspace) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: seed }));
      } else if (message.name === "FocusGroup" && message.args.symbol?.startsWith("PERP.")) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted" }));
      } else {
        server.send(raw);
      }
    });
    server.onMessage(raw => {
      const frame = JSON.parse(raw.toString());
      if (frame.topic === "exec.status") {
        const status: ExecStatus = frame.payload;
        status.masterArmed = false;
        status.venues.push(
          { ...status.venues[0], venue: "alpaca-live", broker: "alpaca", env: "live", connected: false },
          { ...status.venues[0], venue: "mexc-paper", broker: "mexc-sim", env: "paper" },
          { ...status.venues[0], venue: "bingx-virtual", broker: "bingx-vst", env: "paper" },
          { ...status.venues[0], venue: "bingx-live", broker: "bingx-live", env: "live" },
        );
        socket.send(JSON.stringify(frame));
      } else {
        socket.send(raw);
      }
    });
  });
  await page.goto("/?workspace=" + workspace);
  await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
  const ticket = page.getByTestId("panel-tab-ticket").locator(".ledger-header");
  const select = page.getByTestId("venue");
  const account = page.getByTestId("acct-venue");
  const cue = page.getByTestId("hotkey-target-cue");
  await expect(select).toHaveValue("sim-paper");
  const confirmation = page.waitForEvent("dialog");
  await Promise.all([
    select.selectOption("alpaca-live"),
    confirmation.then(async dialog => {
      expect(dialog.message()).toBe("Switch this Link Group from paper to live trading?");
      await dialog.accept();
    }),
  ]);
  await expect(account).toHaveValue("alpaca-live");
  const load = async (symbol: string) => {
    await ticket.getByTestId("panel-symbol").click();
    await page.keyboard.type(symbol);
    await page.keyboard.press("Enter");
  };
  await load("mexc.btc.usdt");
  await expect(select).toHaveValue("");
  await expect(select.locator("option:checked")).toHaveText("Choose a MEXC account");
  await expect(select.locator("option[value='alpaca-live']")).toHaveCount(0);
  await select.selectOption("mexc-paper");
  await expect(account).toHaveValue("mexc-paper");
  await expect(cue).toContainText("mexc-paper");
  await load("bingx.btc.usdt");
  await expect(select.locator("option:checked")).toHaveText("Choose a BingX account");
  await expect(account).toHaveValue("");
  await expect(cue).not.toContainText("mexc-paper");
  await select.selectOption("bingx-virtual");
  await expect(account).toHaveValue("bingx-virtual");
  await expect(cue).toContainText("bingx-virtual");
  await load("binance.btc.usdt");
  await expect(select.locator("option:checked")).toHaveText("Choose a Binance account");
  await expect(select.locator("option")).toHaveCount(1);
  await load("drgo");
  await expect(select).toHaveValue("alpaca-live");
  await expect(account).toHaveValue("alpaca-live");
  await expect(cue).toContainText("alpaca-live");
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await load("mexc.btc.usdt");
  await expect(select).toHaveValue("mexc-paper");
  await load("bingx.btc.usdt");
  await expect(select).toHaveValue("bingx-virtual");
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("VST");
  await Promise.all([
    page.waitForEvent("dialog").then(async dialog => {
      expect(dialog.message()).toBe("Switch this Link Group from paper to live trading?");
      await dialog.dismiss();
    }),
    select.selectOption("bingx-live"),
  ]);
  await expect(select).toHaveValue("bingx-virtual");
  await Promise.all([
    page.waitForEvent("dialog").then(async dialog => {
      expect(dialog.message()).toBe("Switch this Link Group from paper to live trading?");
      await dialog.accept();
    }),
    select.selectOption("bingx-live"),
  ]);
  await expect(account).toHaveValue("bingx-live");
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await load("mexc.btc.usdt");
  await expect(select).toHaveValue("mexc-paper");
  await load("bingx.btc.usdt");
  await expect(select.locator("option:checked")).toHaveText("Confirm live account");
  await expect(account).toHaveValue("");
});

test("fractional eligibility follows the active session", async ({ page, context }) => {
  const workspace = "e2e-fractionable";
  const seed = venueWorkspace(workspace);
  let phase = "rth";
  await context.routeWebSocket("**/ws", socket => {
    const server = socket.connectToServer();
    socket.onMessage(raw => {
      const message = JSON.parse(raw.toString());
      if (message.name === "GetConfig" && message.args.key === "workspace." + workspace) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: seed }));
      } else if (message.name === "QueryVenueInstrumentEligibility" && message.args.venue === "sim-paper") {
        socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload: {
          supported: true, found: true, error: "", fractionable: true, fractionalEhEnabled: false,
          phase, phaseRefreshMs: 250,
        } }));
      } else server.send(raw);
    });
  });
  await page.goto("/?workspace=" + workspace);
  const tile = page.getByTestId("eligibility-fractionable");
  await expect(tile).toHaveAttribute("title", /Regular hours: Yes.*fractionable: true.*fractional_eh_enabled: false/s);
  phase = "post";
  await expect(tile).toHaveAttribute("title", /After-hours: No/);
  phase = "closed";
  await expect(tile).toHaveAttribute("title", /Closed: Unavailable/);
});
