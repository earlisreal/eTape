import { test, expect, type Page } from "@playwright/test";

// Task 4 (E2E for the settings export/import feature). Mirrors
// settings-redesign.spec.ts's conventions: each test picks its own unique
// `?workspace=` name (fresh + blank — no auto-seed) so parallel spec files
// (and tests within this file) never collide, and the Settings modal is
// reached via TopBar's own gear (aria-label "Settings" — NOT the
// "open-settings" testid, which belongs to the Order Ticket panel's own
// gear and only exists once an order-ticket panel is on screen; none of
// these tests apply a preset, so that gear is never mounted here).
async function gotoBlank(page: Page, workspace: string): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
}

async function openGeneral(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "General", exact: true }).click();
}
async function openOrdersHotkeys(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Orders & hotkeys", exact: true }).click();
}

test.describe("settings export/import", () => {
  test("opening Settings -> General renders the layout download/import controls", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-open-layout");
    await openGeneral(page);

    await expect(page.getByTestId("download-json")).toBeVisible();
    await expect(page.getByTestId("import-file")).toBeVisible();
  });

  test("opening Settings -> Orders & hotkeys renders the hotkeys download/import controls", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-open-hotkeys");
    await openOrdersHotkeys(page);

    await expect(page.getByTestId("download-json")).toBeVisible();
    await expect(page.getByTestId("import-file")).toBeVisible();
  });

  test("downloading from General's Layout group triggers a download named etape-layout-<date>.json", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-download-layout");
    await openGeneral(page);

    const downloadPromise = page.waitForEvent("download");
    await page.getByTestId("download-json").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^etape-layout-\d{4}-\d{2}-\d{2}\.json$/);
  });

  test("downloading from Orders & hotkeys triggers a download named etape-hotkeys-<date>.json", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-download-hotkeys");
    await openOrdersHotkeys(page);

    const downloadPromise = page.waitForEvent("download");
    await page.getByTestId("download-json").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^etape-hotkeys-\d{4}-\d{2}-\d{2}\.json$/);
  });

  test("importing a hotkeys-only fixture with an unbound combo adds its template to Orders & hotkeys", async ({ page }) => {
    // applyImport() always runs a native window.confirm before applying —
    // same pattern as smoke.spec.ts's preset-replace-confirmation test:
    // register the auto-accept handler before the click that triggers it.
    page.on("dialog", (d) => { void d.accept(); });

    await gotoBlank(page, "e2e-backup-import-hotkeys");
    await openOrdersHotkeys(page);

    // fixtures/settings-export-hotkeys.json: one PlaceOrderTemplate labeled
    // "Imported Buy" bound to Ctrl+9 — a combo nothing in a fresh app binds.
    await page.getByTestId("import-file").setInputFiles("fixtures/settings-export-hotkeys.json");
    await expect(page.getByTestId("apply-import")).toBeVisible();

    await page.getByTestId("apply-import").click();
    await expect(page.getByRole("alert")).toContainText("Imported hotkeys.");

    // No nav switch needed: the cheat sheet (rendered by OrderSettingsSection,
    // above the hotkeys import panel in this same "Orders & hotkeys" pane)
    // already reflects the shared OrderConfig context's updated state.
    await expect(page.getByTestId("cheat-sheet")).toContainText("Imported Buy");

    // Cleanup (same convention as before — orderConfig is engine-side, shared
    // across every spec file in one `npm run e2e` run): reset now, while the
    // modal is already open to "Orders & hotkeys".
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("reset-confirm").click();
    await page.getByTestId("save").click();

    await page.mouse.click(5, 5); // close via backdrop
  });

  test("importing a layout-only fixture replaces the panel layout", async ({ page }) => {
    page.on("dialog", (d) => { void d.accept(); });

    await gotoBlank(page, "e2e-backup-import-layout");
    await expect(page.locator(".ledger-header")).toHaveCount(0);

    await openGeneral(page);
    await page.getByTestId("import-file").setInputFiles("fixtures/settings-export-layout.json");
    await expect(page.getByTestId("apply-import")).toBeVisible();

    await page.getByTestId("apply-import").click();
    await expect(page.getByRole("alert")).toContainText("Imported layout.");

    await page.mouse.click(5, 5); // close via backdrop
    await expect(page.locator(".ledger-header", { hasText: "Movers" })).toBeVisible({ timeout: 10_000 });
  });

  test("importing a layout via the empty-state 'Import layout' control replaces the panel layout", async ({ page }) => {
    // Same fixture and end state as the Settings-based import test above, but
    // through the new direct entry point in EmptyState — no Settings modal,
    // no Apply-import button, no confirm dialog (the empty state has nothing
    // to lose, so applyWorkspace runs straight through).
    await gotoBlank(page, "e2e-empty-import-layout");
    await expect(page.locator(".ledger-header")).toHaveCount(0);

    await page.getByTestId("empty-import-file").setInputFiles("fixtures/settings-export-layout.json");

    await expect(page.getByRole("alert")).toContainText("Imported layout.");
    await expect(page.locator(".ledger-header", { hasText: "Movers" })).toBeVisible({ timeout: 10_000 });
  });
});
