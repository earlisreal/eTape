import { expect, test } from "@playwright/test";
import type { Workspace } from "../src/chrome/workspace";

test("a Watchlist and Scanner drive independent Monitoring windows", async ({ context, page: launcher }) => {
  test.setTimeout(90_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  let watchlistSymbol: string | undefined;
  let savedChartSymbols: string[] = [];
  let holdMembership = false;
  let scannerWorkspace: string | null = null;
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
        if (holdMembership) return;
        watchlistSymbol ??= frame.payload.symbols[0];
        frame.payload.symbols = watchlistSymbol ? [watchlistSymbol] : [];
        socket.send(JSON.stringify(frame));
      } else if (frame.topic === "scanner.rank" && frame.payload.scannerId === `${scannerWorkspace}/m-scanner`) {
        frame.payload.rows = frame.payload.rows.slice(0, 1);
        socket.send(JSON.stringify(frame));
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
  scannerWorkspace = new URL(scanner.url()).searchParams.get("workspace");
  expect(scannerWorkspace).not.toBe(watchlistWorkspace);
  const scannerSwitch = scanner.getByRole("button", { name: "Following", exact: true });
  await scanner.getByRole("button", { name: "Use this Scanner" }).click();
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
  await page.bringToFront();
  await page.getByTestId("panel-tab-m-scanner").click();
  await page.getByRole("button", { name: /add panel/i }).click();
  await page.getByRole("dialog", { name: "Add panel" }).getByRole("button", { name: /watchlist your pinned/i }).click();
  await page.getByRole("button", { name: "Use this Watchlist" }).click();
  const following = page.getByRole("button", { name: "Following", exact: true });
  const charts = page.locator(".dv-groupview")
    .filter({ has: page.getByTestId("chart-host") }).getByTestId("panel-symbol");
  await expect(following).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTitle("Following 1/4")).toBeVisible();
  await expect(charts).toHaveCount(4);
  await expect.poll(() => savedChartSymbols).toEqual([watchlistSymbol?.slice(3)]);

  holdMembership = true;
  await page.reload();
  await expect(page.getByTitle("Waiting for Watchlist")).toBeVisible();
  await expect(charts.filter({ hasText: watchlistSymbol!.slice(3) })).toHaveCount(1);
  await expect(following).toHaveAttribute("aria-pressed", "true");
  await expect(scannerSwitch).toContainText("Scanner");
  await expect(scannerSwitch).toHaveAttribute("aria-pressed", "true");
});
