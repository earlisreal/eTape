import { expect, test } from "@playwright/test";
import type { Workspace } from "../src/chrome/workspace";

test("saved Watchlist order drives pinned charts across windows", async ({ context, page }) => {
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  let members: Set<string> | undefined;
  let savedChartSymbols: string[] = [];
  let holdMembership = false;
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const frame = JSON.parse(raw.toString());
      if (frame.name === "SetConfig" && frame.args.key === "workspace.monitoring") {
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
        members ??= new Set<string>(frame.payload.symbols.slice(0, 5));
        frame.payload.symbols = frame.payload.symbols.filter((symbol: string) => members?.has(symbol));
        socket.send(JSON.stringify(frame));
      } else socket.send(raw);
    });
  });

  await page.goto("/?workspace=e2e-watchlist-following");
  await page.getByRole("button", { name: /add panel/i }).click();
  await page.getByRole("dialog", { name: "Add panel" }).getByRole("button", { name: /watchlist your pinned/i }).click();
  const watchlist = page.locator("table").filter({ has: page.getByRole("columnheader", { name: "Volume", exact: true }) });
  const rows = watchlist.locator("tbody tr");
  await expect(rows).toHaveCount(5);
  const symbols = [...members!].map((symbol) => symbol.slice(3));

  await page.getByRole("button", { name: "Use this Watchlist as Monitoring Source" }).click();
  await expect(page.getByRole("button", { name: "Disable Watchlist Sync" })).toBeVisible();
  const monitoring = await context.newPage();
  const charts = monitoring.locator(".dv-groupview")
    .filter({ has: monitoring.getByTestId("chart-host") }).getByTestId("panel-symbol");
  await monitoring.goto("/?workspace=monitoring");
  await expect(page.getByTitle("Following 4/4")).toBeVisible();
  await expect(charts).toHaveCount(4);
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(0);

  const dragRow = async (sourceIndex: number, targetIndex: number, position: "before" | "after") => {
    const source = rows.nth(sourceIndex).getByRole("cell").last();
    const target = rows.nth(targetIndex);
    const bounds = (await target.boundingBox())!;
    await source.dragTo(target, {
      targetPosition: { x: bounds.width / 2, y: bounds.height * (position === "before" ? 0.25 : 0.75) },
    });
  };

  await dragRow(4, 3, "before");
  await expect(charts.filter({ hasText: symbols[4] })).toHaveCount(1);
  await expect(charts.filter({ hasText: symbols[3] })).toHaveCount(0);
  const assigned = await charts.allTextContents();
  await dragRow(0, 1, "after");
  await expect(rows.first()).toContainText(symbols[1]);
  await expect(charts).toHaveText(assigned);
  await expect.poll(() => savedChartSymbols.slice().sort()).toEqual(assigned.slice().sort());
  holdMembership = true;
  await monitoring.reload();
  await expect(charts).toHaveText(assigned);
  holdMembership = false;

  await page.getByRole("button", { name: "Disable Watchlist Sync" }).click();
  await expect(page.getByRole("button", { name: "Enable Watchlist Sync" })).toBeVisible();
  await dragRow(4, 3, "before");
  await expect(rows.nth(3)).toContainText(symbols[3]);
  await expect(charts).toHaveText(assigned);
  await page.getByRole("button", { name: "Enable Watchlist Sync" }).click();
  await expect(charts.filter({ hasText: symbols[3] })).toHaveCount(1);

  for (const symbol of symbols) {
    await rows.filter({ has: page.getByText(symbol, { exact: true }) }).click({ button: "right" });
    await page.getByText(`Remove ${symbol} from watchlist`, { exact: true }).click();
  }
  await expect(page.getByTitle("Following 0/4")).toBeVisible();
  await expect(charts).toHaveText(Array<string>(4).fill("—"));
  await expect(monitoring.getByTestId("chart-empty-state")).toHaveCount(4);
  await page.getByRole("button", { name: "Disable Watchlist Sync" }).click();
  await expect(page.getByRole("button", { name: "Enable Watchlist Sync" })).toBeVisible();
  await monitoring.getByRole("button", { name: "Use this Scanner as Monitoring Source" }).click();
  await expect(page.getByRole("button", { name: "Use this Watchlist as Monitoring Source" })).toBeVisible();
});
