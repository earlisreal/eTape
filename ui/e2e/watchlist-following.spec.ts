import { expect, test } from "@playwright/test";
import type { Workspace } from "../src/chrome/workspace";

test("a Watchlist and Scanner drive independent Monitoring windows", async ({ context, page: launcher }) => {
  test.setTimeout(60_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  let members: Set<string> | undefined;
  let savedChartSymbols: string[] = [];
  let holdMembership = false;
  const delayedMembership = new Map<unknown, () => void>();
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
        frame.payload.symbols = frame.payload.symbols.filter((symbol: string) => members?.has(symbol));
        if (holdMembership) delayedMembership.set(socket, () => socket.send(JSON.stringify(frame)));
        else socket.send(JSON.stringify(frame));
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

  await page.getByRole("button", { name: "Use this Watchlist" }).click();
  const following = page.getByRole("button", { name: "Following", exact: true });
  await expect(following).toHaveAttribute("aria-pressed", "true");
  const charts = page.locator(".dv-groupview")
    .filter({ has: page.getByTestId("chart-host") }).getByTestId("panel-symbol");
  await expect(page.getByTitle("Following 4/4")).toBeVisible();
  await expect(charts).toHaveCount(4);
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(0);

  await rows.nth(4).press("Alt+ArrowUp");
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(1);
  await expect(charts.filter({ hasText: symbols[3] })).toHaveCount(0);
  const assigned = await charts.allTextContents();
  await rows.first().press("Alt+ArrowDown");
  await expect(rows.first()).toContainText(symbols[1]);
  await expect(charts).toHaveText(assigned);
  await expect.poll(() => savedChartSymbols.slice().sort()).toEqual(assigned.slice().sort());
  holdMembership = true;
  await page.reload();
  await expect(charts).toHaveText(assigned);
  holdMembership = false;
  for (const deliver of delayedMembership.values()) deliver();
  delayedMembership.clear();

  await following.click();
  await expect(following).toHaveAttribute("aria-pressed", "false");
  await rows.nth(4).press("Alt+ArrowUp");
  await expect(rows.nth(3)).toContainText(symbols[3]);
  await expect(charts).toHaveText(assigned);
  await following.click();
  await expect(charts.filter({ hasText: symbols[3] })).toHaveCount(1);

  for (const symbol of symbols) {
    await rows.filter({ has: page.getByText(symbol, { exact: true }) }).click({ button: "right" });
    await page.getByText(`Remove ${symbol} from watchlist`, { exact: true }).click();
  }
  await expect(page.getByTitle("Following 0/4")).toBeVisible();
  await expect(charts).toHaveText(Array<string>(4).fill("—"));
  await expect(page.getByTestId("chart-empty-state")).toHaveCount(4);
  await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
  await expect(following).toContainText("Watchlist");
  await expect(following).toHaveAttribute("aria-pressed", "true");
  await following.click();
  await expect(following).toHaveAttribute("aria-pressed", "false");
  await expect(scannerSwitch).toContainText("Scanner");
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
  await scanner.reload();
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
});
