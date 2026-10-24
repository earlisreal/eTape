import { test, expect } from "@playwright/test";

test("settings and popups share dismissal, focus and a responsive light/dark theme", async ({ page }, testInfo) => {
  await page.goto("/?workspace=e2e-ui-consistency");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  const settingsButton = page.getByRole("button", { name: "Settings", exact: true });
  await settingsButton.click();
  const settings = page.getByRole("dialog", { name: "Settings", exact: true });
  await expect(settings).toBeVisible();
  await expect(settings.getByRole("button", { name: "General", exact: true })).toHaveAttribute("aria-current", "page");
  await page.screenshot({ path: testInfo.outputPath("settings-light.png"), animations: "disabled" });

  await settings.getByRole("radio", { name: "Dark", exact: true }).check();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.screenshot({ path: testInfo.outputPath("settings-dark.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  await expect(settings).toBeHidden();
  await expect(settingsButton).toBeFocused();

  await settingsButton.click();
  await page.mouse.click(3, 3);
  await expect(settings).toBeHidden();

  const add = page.getByRole("button", { name: "+ Add panel", exact: true });
  const menu = page.getByRole("dialog", { name: "Add panel", exact: true });
  await add.click();
  await expect(menu).toBeVisible();
  await add.click();
  await expect(menu).toBeHidden();
  await add.click();
  await page.mouse.click(3, 100);
  await expect(menu).toBeHidden();
  await add.click();
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(add).toBeFocused();

  await settingsButton.click();
  await settings.getByRole("button", { name: "Venues & creds", exact: true }).click();
  await expect(settings.getByTestId("sim-card")).toBeVisible();
  await settings.getByRole("button", { name: "General", exact: true }).click();
  await page.setViewportSize({ width: 600, height: 760 });
  const bounds = await settings.boundingBox();
  expect(bounds).toBeTruthy();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(600);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(760);
  await page.screenshot({ path: testInfo.outputPath("settings-narrow.png"), animations: "disabled" });
  await settings.getByRole("radio", { name: "Light", exact: true }).check();
  await settings.getByRole("button", { name: "Done", exact: true }).click();
  await expect(settings).toBeHidden();
});

test("import confirmation closes without replacing settings and leaves its parent open", async ({ page }) => {
  await page.goto("/?workspace=e2e-ui-import-dismiss");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const settings = page.getByRole("dialog", { name: "Settings", exact: true });
  await settings.getByRole("button", { name: "Orders & hotkeys", exact: true }).click();
  await settings.getByTestId("import-file").setInputFiles("fixtures/settings-export-hotkeys.json");
  await settings.getByTestId("apply-import").click();
  const confirmation = page.getByRole("dialog", { name: "Import hotkeys", exact: true });
  await expect(confirmation).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(confirmation).toBeHidden();
  await expect(settings).toBeVisible();
  await expect(settings.getByTestId("apply-import")).toBeFocused();
  await expect(settings.getByText("Imported Buy", { exact: true })).toHaveCount(0);
  await settings.getByRole("button", { name: "Close settings" }).click();
});

test("chart settings, workspace and practice dialogs use the shared surface", async ({ page }, testInfo) => {
  await page.goto("/?workspace=e2e-ui-dialog-surfaces");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "+ Add panel", exact: true }).click();
  await page.getByRole("dialog", { name: "Add panel", exact: true }).getByText("Chart", { exact: true }).click();
  const gear = page.getByRole("button", { name: "chart settings", exact: true });
  await gear.click();
  const chart = page.getByRole("dialog", { name: "Chart settings", exact: true });
  await expect(chart).toBeVisible();
  await expect(chart.getByRole("button", { name: "Ok", exact: true })).toHaveClass(/btn-primary/);
  await page.screenshot({ path: testInfo.outputPath("chart-settings.png"), animations: "disabled" });
  await page.mouse.click(3, 3);
  await expect(chart).toBeHidden();
  await expect(gear).toBeFocused();

  await page.getByRole("button", { name: /New window/ }).click();
  const workspace = page.getByRole("dialog", { name: "New window", exact: true });
  await expect(workspace.getByRole("textbox", { name: "Workspace name", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(workspace).toBeHidden();

  await page.getByRole("button", { name: "Practice", exact: true }).click();
  const practice = page.getByRole("dialog", { name: "Practice", exact: true });
  await expect(practice.getByRole("button", { name: "Start demo market" })).toHaveClass(/btn-primary/);
  await practice.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(practice).toBeHidden();
});
