import { test, expect } from "@playwright/test";

test("EOD Screener runs in demo, preserves filters and presets, and supports a narrow layout", async ({ page }, testInfo) => {
	let saveCorrelation = "", presetSaved = false, engineReady = false, historyReady = false;
	page.on("websocket", socket => {
		socket.on("framesent", event => {
			const message = JSON.parse(String(event.payload));
			if (message.name === "SetConfig" && message.args?.key === "workspace.e2e-screener"
				&& JSON.stringify(message.args.value).includes("Six month review")) saveCorrelation = message.corrId;
		});
		socket.on("framereceived", event => {
			const message = JSON.parse(String(event.payload));
			if (message.topic === "sys.boot" && message.payload?.phase === "ready") engineReady = true;
			if (message.topic === "watchlist.rows" && message.payload?.symbols?.length > 0) historyReady = true;
			if (message.kind === "ack" && message.corrId === saveCorrelation && message.status === "accepted") presetSaved = true;
		});
	});
  await page.goto("/?workspace=e2e-screener");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await expect.poll(() => engineReady && historyReady, { timeout: 20_000 }).toBe(true);
  await page.getByRole("button", { name: "+ Add panel" }).click();
  await page.getByLabel("Add panel", { exact: true }).getByRole("button", { name: /Screener End-of-day swing screens/ }).click();
  const panel = page.locator("[data-screener-panel]");
  await expect(panel).toBeVisible();
  await panel.getByLabel("High lookback").selectOption("6-month");
  await panel.getByLabel("Within % of high").fill("100");
  await panel.getByRole("button", { name: "Run screen" }).click();
  await expect(panel.getByRole("status")).toContainText("matches", { timeout: 20_000 });
  await expect(panel.locator("tbody tr").first()).toBeVisible();
  await expect(panel.locator("tbody tr").first().locator("td").nth(5)).not.toHaveText("0");
  await expect(panel.getByRole("status")).toContainText("Demo: generated daily history");
  await panel.getByText("Save a preset", { exact: true }).click();
  await panel.getByLabel("Preset name").fill("Six month review");
  await panel.getByRole("button", { name: "Save preset" }).click();
  await panel.getByLabel("Screener preset").selectOption("builtin-new");
  await expect(panel.getByLabel("High condition")).toHaveValue("new");
  await panel.getByLabel("Screener preset").selectOption("saved:Six month review");
  await expect(panel.getByLabel("High lookback")).toHaveValue("6-month");
  await expect.poll(() => presetSaved).toBe(true);
  await page.reload();
  await page.locator('[data-testid^="panel-tab-screener-"]').click();
  await expect(panel.getByLabel("High lookback")).toHaveValue("6-month");
  await expect(panel.getByLabel("Screener preset").getByRole("option", { name: "Six month review" })).toHaveCount(1);
  await page.screenshot({ path: testInfo.outputPath("screener-wide.png"), fullPage: true });
  await page.setViewportSize({ width: 600, height: 800 });
  await expect(panel.getByRole("button", { name: "Run screen" })).toBeVisible();
  await expect(panel.getByLabel("High lookback")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("screener-narrow.png"), fullPage: true });
});
