import { test, expect } from "@playwright/test";

test("replay engine shows the REPLAY banner and launcher lists the recorded day", async ({ page }) => {
  await page.goto("/?workspace=e2e-replay");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  // serve.sh boots -replay 2026-01-02, so the banner must be present.
  await expect(page.getByTestId("replay-banner")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("replay-banner")).toContainText("2026-01-02");
  // Launcher lists the recorded day from ListReplayDays.
  await page.getByRole("button", { name: /Practice/ }).click();
  await expect(page.getByTestId("replay-launcher")).toBeVisible();
  await expect(page.getByTestId("replay-day")).toContainText("2026-01-02");
});
