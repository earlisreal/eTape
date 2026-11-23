import { expect, test, type WebSocketRoute } from "@playwright/test";
import type { Workspace } from "../src/chrome/workspace";

test("a Watchlist and Scanner drive independent Monitoring windows", async ({ context, page: launcher }) => {
  test.setTimeout(90_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  let members: Set<string> | undefined;
  let savedChartSymbols: string[] = [];
  let holdMembership = false;
  const membershipFeeds = new Map<WebSocketRoute, () => void>();
  let membershipOverride: string[] | undefined;
  let watchlistWorkspace: string | null = null;
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const frame = JSON.parse(raw.toString());
      if (frame.name === "SetConfig" && frame.args.key === `workspace.${watchlistWorkspace}`) {
        const workspace = frame.args.value as Workspace;
        savedChartSymbols = workspace.panels.flatMap((panel) =>
          panel.panelId === "chart" && typeof panel.settings.symbol === "string" ? [panel.settings.symbol.slice(3)] : []);
      }
      server.send(raw);
    });
    server.onMessage((raw) => {
      const frame = JSON.parse(raw.toString());
      if (frame.topic === "sys.session" && frame.payload) {
        frame.payload.mode = "live";
        socket.send(JSON.stringify(frame));
      } else if (frame.topic === "watchlist.rows") {
        members ??= new Set<string>(frame.payload.symbols.slice(0, 5));
        const membership = frame.payload.symbols.filter((symbol: string) => members?.has(symbol));
        const deliver = () => socket.send(JSON.stringify({
          ...frame, payload: { ...frame.payload, symbols: membershipOverride ?? membership },
        }));
        membershipFeeds.set(socket, deliver);
        if (!holdMembership) deliver();
      } else socket.send(raw);
    });
  });

  await launcher.goto("/");
  const openMonitor = async (name: string) => {
    await launcher.getByRole("button", { name: /New window/ }).click();
    await launcher.getByLabel("Workspace name").fill(name);
    await launcher.getByLabel("Starting layout").selectOption("monitoring");
    const popup = launcher.waitForEvent("popup");
    await launcher.getByRole("button", { name: "Create new" }).click();
    const window = await popup;
    await expect(window.getByRole("button", { name: "Use this Scanner" })).toBeVisible();
    return window;
  };
  const page = await openMonitor("Watchlist desk");
  watchlistWorkspace = new URL(page.url()).searchParams.get("workspace");
  const scanner = await openMonitor("Scanner desk");
  const scannerSwitch = scanner.getByRole("button", { name: "Following", exact: true });
  await scanner.getByRole("button", { name: "Use this Scanner" }).click();
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
  await page.bringToFront();
  await page.getByTestId("panel-tab-m-scanner").click();
  await page.getByRole("button", { name: /add panel/i }).click();
  await page.getByRole("dialog", { name: "Add panel" }).getByRole("button", { name: /watchlist your pinned/i }).click();
  const watchlist = page.locator("table").filter({ has: page.getByRole("columnheader", { name: "Volume", exact: true }) });
  const rows = watchlist.locator("tbody tr");
  await expect(rows).toHaveCount(5);
  const symbols = [...members!].map((symbol) => symbol.slice(3));
  const setMembership = (order: string[]) => {
    membershipOverride = order.map((symbol) => `US.${symbol}`);
    for (const deliver of membershipFeeds.values()) deliver();
  };

  await page.getByRole("button", { name: "Use this Watchlist" }).click();
  const following = page.getByRole("button", { name: "Following", exact: true });
  await expect(following).toHaveAttribute("aria-pressed", "true");
  const charts = page.locator(".dv-groupview")
    .filter({ has: page.getByTestId("chart-host") }).getByTestId("panel-symbol");
  await expect(page.getByTitle("Following 4/4")).toBeVisible();
  await expect(charts).toHaveCount(4);
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(0);

  setMembership([symbols[0], symbols[1], symbols[2], symbols[4], symbols[3]]);
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(1);
  await expect(charts.filter({ hasText: symbols[3] })).toHaveCount(0);
  // Demo quote updates can add a status suffix to the displayed ticker.
  const displayedSymbols = async () => (await charts.allTextContents()).map((symbol) => symbol.replace(/[*]+$/, ""));
  const assigned = await displayedSymbols();
  setMembership([symbols[1], symbols[0], symbols[2], symbols[4], symbols[3]]);
  await expect(rows.first()).toContainText(symbols[1]);
  await expect.poll(displayedSymbols).toEqual(assigned);
  await expect.poll(() => savedChartSymbols.slice().sort()).toEqual(assigned.slice().sort());
  holdMembership = true;
  await page.reload();
  await expect.poll(displayedSymbols).toEqual(assigned);
  await expect(following).toHaveAttribute("aria-pressed", "true");
  await expect(scannerSwitch).toContainText("Scanner");
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
});
