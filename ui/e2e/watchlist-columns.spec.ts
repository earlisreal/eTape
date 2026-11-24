import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import type { Workspace } from "../src/chrome/workspace";

test("Watchlist session columns persist independently and survive layout export and import", async ({ context, page }) => {
  test.setTimeout(90_000);
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  let saved: Workspace | undefined;
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const frame = JSON.parse(raw.toString());
      if (frame.name === "SetConfig" && frame.args.key === "workspace.e2e-watchlist-columns") {
        saved = frame.args.value as Workspace;
      }
      server.send(raw);
    });
    server.onMessage((raw) => {
      const frame = JSON.parse(raw.toString());
      if (frame.kind === "delta" && frame.topic?.startsWith("md.")) return;
      if (frame.topic === "sys.session" && frame.payload) frame.payload.mode = "live";
      if (frame.topic === "watchlist.rows") {
        frame.payload = {
          refreshedAt: new Date().toISOString(), session: "post", symbols: ["US.AAPL"],
          rows: [{ symbol: "US.AAPL", last: 100, changePct: 3, volume: 20000, sessionChangePct: -1.2, sessionVolume: 4000 }],
        };
      }
      socket.send(JSON.stringify(frame));
    });
  });

  const addWatchlist = async (id: string) => {
    await page.goto(`/?workspace=${id}`);
    await page.getByRole("button", { name: /add panel/i }).first().click();
    await page.getByRole("dialog", { name: "Add panel" }).getByRole("button", { name: /watchlist your pinned/i }).click();
    await expect(page.getByRole("columnheader")).toHaveCount(6);
  };

  await page.setViewportSize({ width: 480, height: 600 });
  await addWatchlist("e2e-watchlist-columns");
  await expect(page.getByText("After-hours", { exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "−1.2%", exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: "4K", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Columns", exact: true }).click();
  await page.getByRole("checkbox", { name: "Session Volume" }).uncheck();
  await expect(page.getByRole("columnheader", { name: "Session Volume", exact: true })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect.poll(() => saved?.panels.find((panel) => panel.panelId === "watchlist")?.settings.watchlistHiddenColumns)
    .toEqual(["sessionVolume"]);
  await page.reload();
  await expect(page.getByRole("columnheader")).toHaveCount(5);

  await page.setViewportSize({ width: 1000, height: 750 });
  const openGeneral = async () => {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "General", exact: true }).click();
  };
  await openGeneral();
  const downloading = page.waitForEvent("download");
  await page.getByTestId("download-json").click();
  const download = await downloading;
  const buffer = await readFile(await download.path());

  await addWatchlist("e2e-watchlist-columns-import");
  await openGeneral();
  await page.getByTestId("import-file").setInputFiles({
    name: download.suggestedFilename(),
    mimeType: "application/json",
    buffer,
  });
  await page.getByTestId("apply-import").click();
  await page.getByRole("button", { name: "Replace layout", exact: true }).click();
  await expect(page.getByRole("alert").filter({ hasText: "Imported layout." })).toBeVisible();
  await page.getByRole("button", { name: "Close settings" }).click();
  await expect(page.getByRole("columnheader")).toHaveCount(5);
});
