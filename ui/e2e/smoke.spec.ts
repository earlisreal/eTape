import { test, expect } from "@playwright/test";

test("engine serves the production bundle and the account bar hydrates", async ({ page }) => {
  await page.goto("/?workspace=trading");
  await expect(page.locator("#root")).toBeVisible();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
});
