import { test, expect } from "@playwright/test";
import type { ClientMessage, ScannerRankPayload, ServerMessage } from "../src/wire/contract";
import { DEFAULT_SCANNER_FILTERS } from "../src/chrome/scannerFilters";
import { WORKSPACE_LAYOUT_VERSION, type Workspace } from "../src/chrome/workspace";

for (const sync of [false, true]) {
  test(`Scanner divider follows the drag during updates with Scanner Sync ${sync ? "on" : "off"}`, async ({ page }) => {
    await page.setViewportSize({ width: 874, height: 610 });
    await page.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
    const workspace = sync ? "monitoring" : "e2e-scanner-resize";
    let saved: Workspace = {
      name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
      panels: [
        { id: "resize-chart", panelId: "chart", group: null, settings: {} },
        { id: "resize-scanner", panelId: "scanner", group: null, settings: { scannerFilters: DEFAULT_SCANNER_FILTERS } },
      ],
      scannerSync: { enabled: sync, sourceWorkspaceId: workspace, sourcePanelId: "resize-scanner" },
    };
    let pushRank: (iteration: number) => void;
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.routeWebSocket("**/ws", (socket) => {
      const server = socket.connectToServer();
      socket.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ClientMessage;
        if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
          socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: saved }));
        } else {
          if (message.kind === "command" && message.name === "SetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
            saved = (message.args as { value: Workspace }).value;
          }
          server.send(raw);
        }
      });
      server.onMessage((raw) => {
        const message = JSON.parse(raw.toString()) as ServerMessage;
        if ((message.kind === "snapshot" || message.kind === "delta") && message.topic.startsWith("scanner.")) return;
        if (message.kind === "snapshot" && message.topic === "sys.session") {
          socket.send(JSON.stringify({ ...message, payload: { ...message.payload as object, mode: "live" } }));
        } else socket.send(raw);
      });
      pushRank = (iteration) => {
        const payload: ScannerRankPayload = {
          scannerId: `${workspace}/resize-scanner`, session: "premarket", status: "ready",
          refreshedAt: new Date().toISOString(), discoveryAt: new Date().toISOString(),
          rows: Array.from({ length: 30 }, (_, index) => ({
            symbol: `US.S${iteration}${index}`, shortSellRestricted: false, changePct: 100 - index,
            last: 0.5, floatShares: 84_600_000, volume: 410_000, sessionVolume: 38_600_000,
            turnover: 114_400, relativeVolume: 0.87, shortInterest: 494_100, shortInterestAsOf: null,
          })),
        };
        socket.send(JSON.stringify({ kind: "delta", topic: "scanner.rank", payload }));
      };
    });

    await page.goto(`/?workspace=${workspace}`);
    const summary = page.getByTestId("scanner-filter-summary");
    await expect(summary).toBeVisible();
    await expect(page.getByTestId("boot-status-banner")).toHaveCount(0);
    const scanner = summary.locator("xpath=ancestor::*[@data-testid='panel-body']");
    const update = async (iteration: number) => {
      pushRank(iteration);
      await expect(scanner.getByText(`S${iteration}0`, { exact: true })).toBeVisible();
      if (sync) await expect(page.getByTestId("panel-symbol")).toHaveText(`S${iteration}0`);
      await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
    };
    await update(0);
    const before = (await scanner.boundingBox())!;
    const y = before.y + before.height / 2;
    await page.mouse.move(before.x, y);
    await page.mouse.down();
    const offsets = [-30, -100, -180, -100, 50];
    for (const [index, offset] of offsets.entries()) {
      await page.mouse.move(before.x + offset, y);
      await update(index + 1);
      expect((await scanner.boundingBox())!.x).toBeCloseTo(before.x + offset, 0);
    }
    await page.mouse.up();
    await update(6);
    const persistedX = before.x + offsets.at(-1)!;
    expect((await scanner.boundingBox())!.x).toBeCloseTo(persistedX, 0);
    await expect.poll(() => saved.layout).not.toBeNull();
    // Let the workspace debounce finish before reopening the saved layout.
    await page.waitForTimeout(600);
    await page.reload();
    await expect(summary).toBeVisible();
    await update(7);
    expect((await scanner.boundingBox())!.x).toBeCloseTo(persistedX, 0);
    expect(errors).toEqual([]);
  });
}
