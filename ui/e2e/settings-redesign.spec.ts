import { test, expect, type Page } from "@playwright/test";

// Blank-start workspace model (Task 7/10): `?workspace=<name>` always starts
// empty; reaching a populated layout means clicking a preset card in the
// empty-state Catalog. Local copy of the same helper smoke.spec.ts and
// error-matrix.spec.ts each keep, so this file's runs stay isolated from
// theirs (each test picks its own workspace name).
async function gotoAndApplyPreset(page: Page, workspace: string, presetName: "Trading" | "Monitoring"): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: new RegExp(`^${presetName}`) }).click();
}

test.describe("settings redesign", () => {
  test("orders: a template's size edit round-trips and changes the fired order qty", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-settings-orders", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // Applying a preset never seeds the shared LinkGroups focus — each blue
    // panel's displayed "AAPL" is a purely local creation-time fallback
    // (PanelFrame's `rawSymbol = symbol ?? config.settings.symbol`), and the
    // group's shared focus stays unset until a real type-to-load commit
    // (verified by reading PanelFrame.tsx/linkGroups.ts: nothing calls
    // linkGroups.focus/hydrate from applyPreset). useHotkeys.ts has no such
    // local fallback — it reads only linkGroups.symbolFor(group) — so without
    // an explicit commit here, Ctrl+1 below would resolve an empty symbol and
    // silently no-op with a "no venue/quote for hotkey" toast, even though the
    // ticket/DOM/tape already display a live AAPL quote. Mirrors smoke.spec.ts's
    // "link groups" test. Round-trips through a decoy symbol (US.MSFT —
    // accepted by the engine's FocusGroup probe, a no-op in replay mode since
    // there's no live feed to validate against; never actually traded) so each
    // commit produces an observable header-text change to wait on: typing
    // "AAPL" while the header already reads "AAPL" would give Playwright
    // nothing to poll, racing the async FocusGroup ack.
    const domHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
    const domPanel = domHeader.locator("xpath=..");
    await domPanel.getByTestId("panel-body").click();
    await expect(domPanel).toHaveClass(/panel-focused/);
    await page.keyboard.type("MSFT");
    await page.keyboard.press("Enter");
    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("MSFT", { timeout: 10_000 });
    await page.keyboard.type("AAPL");
    await page.keyboard.press("Enter");
    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("AAPL", { timeout: 10_000 });

    // Arm the master switch (same as smoke.spec's paper-fill test) — the hotkey
    // engine (useHotkeys.ts) refuses to fire while disarmed. Per-venue arm was
    // removed; master arm + the risk-limit gate are the only checks now.
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("ARMED");

    // The default "Buy $5k" template (id "buy-5k", hotkey Ctrl+1) prices off the
    // live Ask with zero offset (DEFAULT_TEMPLATES in actionTemplate.ts), so the
    // fired qty is floor(dollarAmount / ask). Read the ticket's own ask readout
    // (OrderTicketPanel's `ask` testid) to compute the expected qty independently
    // of this test rather than guessing a price — replay runs with -speed 0
    // -replay-hold, so the quote is static for the whole suite.
    const askLocator = page.getByTestId("ask");
    await expect(askLocator).not.toHaveText("—", { timeout: 15_000 });
    const ask = Number(await askLocator.innerText());
    expect(ask).toBeGreaterThan(0);

    const qtyAt5k = Math.floor(5000 / ask);
    const qtyAt7k = Math.floor(7000 / ask);
    expect(qtyAt7k).not.toBe(qtyAt5k); // sanity: the edit below must actually move the fired size

    // Fire the untouched default binding and capture its flash toast (pushed by
    // OrderCommands.submit in commands.ts, rendered as a role="alert" Toast).
    await page.locator("body").click(); // ensure document focus; useHotkeys bails if !document.hasFocus()
    await page.keyboard.press("Control+1");
    await expect(page.getByText(new RegExp(`^BUY ${qtyAt5k.toLocaleString("en-US")} AAPL @`))).toBeVisible({ timeout: 10_000 });

    // Edit the template's dollar amount via the order ticket's own gear, which
    // (OpenSettingsContext.openOrderSettings) jumps straight to the "Orders &
    // hotkeys" settings section — no need to navigate the modal's nav tabs.
    await page.getByTestId("open-settings").click();
    await page.getByLabel("size-value-buy-5k").fill("7000");
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5); // click the modal backdrop to close (SettingsModal has no Escape handler)

    // Re-fire the same hotkey binding; the qty must now reflect the edited
    // $7000, not the original $5000 — this is the actual round-trip: settings
    // UI -> SetConfig("orderConfig") -> shared OrderConfigProvider context ->
    // useHotkeys reads the same config -> resolvePlaceTemplate recomputes size.
    await page.keyboard.press("Control+1");
    await expect(page.getByText(new RegExp(`^BUY ${qtyAt7k.toLocaleString("en-US")} AAPL @`))).toBeVisible({ timeout: 10_000 });

    // Cleanup: the orderConfig key and arm state are engine-side (shared across
    // every spec file in one `npm run e2e` run, since webServer boots ONE engine
    // for the whole invocation). Restore both so smoke.spec.ts's arm test (which
    // expects to transition disarmed -> ARMED) and any hotkey-dependent test
    // that runs after this file aren't left holding a mutated template or an
    // already-armed gate.
    await page.getByTestId("open-settings").click();
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("reset-confirm").click();
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5);
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("DISARMED");
  });

  test("venues: an invalid venue id surfaces a validation error inline", async ({ page }) => {
    // No preset needed — TopBar (and its Settings gear) is always mounted, even
    // against the blank/empty-state workspace (AppShell renders TopBar outside
    // the ws.panels.length check). This test never arms anything or touches an
    // order; it only exercises the file-only venue-admin write path (Task 3/4/10),
    // which the engine rejects before writing on any validation failure.
    await page.goto("/?workspace=e2e-settings-venues");
    await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });

    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Venues & creds", exact: true }).click();

    // e2e/serve.sh seeds config.toml with one venue ("sim-paper"), so the newly
    // added row lands at whatever index is next, not necessarily 0 — count the
    // existing rows instead of assuming an empty list.
    const venueIdInputs = page.locator('[data-testid^="venue-id-"]');
    const newIndex = await venueIdInputs.count();
    await page.getByTestId("add-venue").click();

    // Illegal chars: config.ValidateVenueConfig enforces venue id ~= ^[a-z0-9-]+$
    // (engine/internal/config/config.go); this must be rejected server-side
    // before anything is written to config.toml.
    await page.getByTestId(`venue-id-${newIndex}`).fill("Bad Id!");
    await page.getByTestId("save-venues").click();
    await expect(page.getByTestId("venues-error")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId("venues-error")).toContainText(/must be non-empty and match/i);
  });
});
