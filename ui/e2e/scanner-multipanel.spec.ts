import { test, expect, type Locator, type Page } from "@playwright/test";

async function addScanner(page: Page): Promise<void> {
  await page.getByRole("button", { name: "+ Add panel" }).click();
  await page.getByRole("button", { name: /Scanner Live gappers/ }).first().click();
}

async function setPanelFilters(
  page: Page,
  tab: Locator,
  mode: "gainers" | "session_volume",
  threshold: string,
): Promise<void> {
  await tab.click();
  await page.getByRole("button", { name: "filters" }).click();
  await page.getByLabel("rank mode").selectOption(mode);
  if (mode === "gainers") await page.getByLabel("min gain %").fill(threshold);
  else await page.getByLabel("min session volume").fill(threshold);
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByTestId("scanner-filter-summary")).toContainText(
    mode === "gainers" ? `change magnitude ≥ ${threshold}%` : `session vol ≥ ${threshold}k`,
  );
}

test("Scanner panels keep their filters independent in the demo engine", async ({ page }) => {
  await page.goto("/?workspace=e2e-independent-scanners");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await addScanner(page);
  await addScanner(page);

  const scannerTabs = page.locator('[data-testid^="panel-tab-scanner-"]');
  await expect(scannerTabs).toHaveCount(2);
  const first = scannerTabs.nth(0);
  const second = scannerTabs.nth(1);

  await setPanelFilters(page, first, "gainers", "8");
  await setPanelFilters(page, second, "session_volume", "500");

  await first.click();
  await page.getByRole("button", { name: "filters" }).click();
  await expect(page.getByLabel("rank mode")).toHaveValue("gainers");
  await expect(page.getByLabel("min gain %")).toHaveValue("8");
  await page.keyboard.press("Escape");

  await second.click();
  await page.getByRole("button", { name: "filters" }).click();
  await expect(page.getByLabel("rank mode")).toHaveValue("session_volume");
  await expect(page.getByLabel("min session volume")).toHaveValue("500");
});
