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

async function openBackupSection(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Import & export", exact: true }).click();
}

test.describe("settings export/import", () => {
  test("opening Settings -> Import & export renders the download/import controls", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-open");
    await openBackupSection(page);

    await expect(page.getByTestId("export-layout")).toBeVisible();
    await expect(page.getByTestId("export-hotkeys")).toBeVisible();
    await expect(page.getByTestId("download-json")).toBeVisible();
    await expect(page.getByTestId("import-file")).toBeVisible();
  });

  test("downloading with both checkboxes checked triggers a download named etape-settings-<date>.json", async ({ page }) => {
    await gotoBlank(page, "e2e-backup-download");
    await openBackupSection(page);

    // Both checkboxes default to checked (BackupSection's own useState(true))
    // on every fresh mount of the section — assert that rather than forcing
    // it, so this test also catches a regression that flips either default.
    await expect(page.getByTestId("export-layout")).toBeChecked();
    await expect(page.getByTestId("export-hotkeys")).toBeChecked();

    const downloadPromise = page.waitForEvent("download");
    await page.getByTestId("download-json").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^etape-settings-\d{4}-\d{2}-\d{2}\.json$/);
  });

  test("importing a hotkeys-only fixture with an unbound combo adds its template to Orders & hotkeys", async ({ page }) => {
    // applyImport() always runs a native window.confirm before applying —
    // same pattern as smoke.spec.ts's preset-replace-confirmation test:
    // register the auto-accept handler before the click that triggers it.
    page.on("dialog", (d) => { void d.accept(); });

    await gotoBlank(page, "e2e-backup-import-hotkeys");
    await openBackupSection(page);

    // fixtures/settings-export-hotkeys.json: one PlaceOrderTemplate labeled
    // "Imported Buy" bound to Ctrl+9 — a combo nothing in a fresh app binds.
    await page.getByTestId("import-file").setInputFiles("fixtures/settings-export-hotkeys.json");
    await expect(page.getByTestId("import-hotkeys")).toBeVisible();
    await expect(page.getByTestId("apply-import")).toBeVisible();

    await page.getByTestId("apply-import").click();
    await expect(page.getByRole("alert")).toContainText("Imported hotkeys.");

    // Switch nav within the still-open modal (no need to close/reopen):
    // OrderSettingsSection remounts fresh reading the now-updated shared
    // OrderConfig context (useOrderConfig's save() is synchronous), so the
    // imported template's label shows up immediately in the cheat sheet
    // (rendered for every template that has a bound hotkey).
    await page.getByRole("button", { name: "Orders & hotkeys", exact: true }).click();
    await expect(page.getByTestId("cheat-sheet")).toContainText("Imported Buy");

    // Cleanup: the orderConfig key is engine-side, shared across every spec
    // file in one `npm run e2e` run (webServer boots ONE engine for the whole
    // invocation) — same convention as settings-redesign.spec.ts's "orders"
    // test. Reset now, while the modal is already open to "Orders & hotkeys",
    // so the imported "Imported Buy"/Ctrl+9 template doesn't survive into a
    // later spec file's hotkey assumptions.
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("reset-confirm").click();
    await page.getByTestId("save").click();

    await page.mouse.click(5, 5); // close via backdrop (SettingsModal has no Escape handler)
  });

  test("importing a layout-only fixture replaces the panel layout", async ({ page }) => {
    page.on("dialog", (d) => { void d.accept(); });

    await gotoBlank(page, "e2e-backup-import-layout");
    // A blank `?workspace=` starts with zero panels (EmptyState/Catalog, no
    // dockview mount at all) — confirm there is no "Movers" panel (or any
    // panel) before the import, so the post-import assertion is a genuine
    // before/after, not something that was already there.
    await expect(page.locator(".ledger-header")).toHaveCount(0);

    await openBackupSection(page);
    // fixtures/settings-export-layout.json: a single "movers" panel — going
    // from zero panels to one exercises AppShell's "first mount" dockview
    // seed path (onReady's `event.api.fromJSON(ws.layout)`), since dockview
    // isn't mounted yet while the workspace is empty.
    await page.getByTestId("import-file").setInputFiles("fixtures/settings-export-layout.json");
    await expect(page.getByTestId("import-layout")).toBeVisible();
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
