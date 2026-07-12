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

    // eTape ships with NO default order templates/hotkeys (DEFAULT_TEMPLATES in
    // actionTemplate.ts is empty) — build one from scratch: a Dollar/$5000 Buy
    // at live Ask with zero offset, bound to Ctrl+1. On an empty template list
    // uid() deterministically mints "tmpl-1-1" for the first add.
    await page.getByTestId("open-settings").click();
    await page.getByTestId("add-template").click();
    await page.getByTestId("add-place").click();
    await page.getByLabel("size-mode-tmpl-1-1").selectOption("Dollar");
    await page.getByLabel("size-value-tmpl-1-1").fill("5000");
    await page.getByTestId("tmpl-hotkey-tmpl-1-1").click();
    await page.keyboard.press("Control+1");
    await expect(page.getByTestId("tmpl-hotkey-tmpl-1-1")).toHaveValue("Ctrl+1");
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5); // click the modal backdrop to close (SettingsModal has no Escape handler)

    // addPlace's own defaults (side BUY, type LIMIT, tif DAY, priceSource Ask,
    // priceOffset 0 — see OrderSettingsSection.tsx) already match the old
    // "Buy $5k" template, so the fired qty is floor(dollarAmount / ask). Read
    // the ticket's own ask readout (OrderTicketPanel's `ask` testid) to compute
    // the expected qty independently of this test rather than guessing a price
    // — replay runs with -speed 0 -replay-hold, so the quote is static for the
    // whole suite.
    const askLocator = page.getByTestId("ask");
    await expect(askLocator).not.toHaveText("—", { timeout: 15_000 });
    const ask = Number(await askLocator.innerText());
    expect(ask).toBeGreaterThan(0);

    const qtyAt5k = Math.floor(5000 / ask);
    const qtyAt7k = Math.floor(7000 / ask);
    expect(qtyAt7k).not.toBe(qtyAt5k); // sanity: the edit below must actually move the fired size

    // Fire the newly-bound template and capture its flash toast (pushed by
    // OrderCommands.submit in commands.ts, rendered as a role="alert" Toast).
    await page.locator("body").click(); // ensure document focus; useHotkeys bails if !document.hasFocus()
    await page.keyboard.press("Control+1");
    await expect(page.getByText(new RegExp(`^BUY ${qtyAt5k.toLocaleString("en-US")} AAPL @`))).toBeVisible({ timeout: 10_000 });

    // Edit the template's dollar amount via the order ticket's own gear, which
    // (OpenSettingsContext.openOrderSettings) jumps straight to the "Orders &
    // hotkeys" settings section — no need to navigate the modal's nav tabs.
    await page.getByTestId("open-settings").click();
    await page.getByLabel("size-value-tmpl-1-1").fill("7000");
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
    // for the whole invocation). reset-defaults now restores the blank baseline
    // (no default templates/hotkeys) — the tmpl-1-1 template built above must
    // not survive into later specs. Restore both this and arm state so
    // smoke.spec.ts's arm test (which expects to transition disarmed -> ARMED)
    // and any hotkey-dependent test that runs after this file aren't left
    // holding a stray template or an already-armed gate.
    await page.getByTestId("open-settings").click();
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("reset-confirm").click();
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5);
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("DISARMED");
  });

  // The venues broker-cards redesign (§C) replaced the generic "Add venue" +
  // user-typed venue-id row model with a fixed 4-card roster and no id input
  // anywhere in the form — the old "invalid venue id" test below no longer
  // has anything to exercise (there's no id field left to mistype) and is
  // replaced by roster/slot-model coverage that fits the new UI.
  //
  // NOT covered here (flagged rather than silently dropped): the moomoo
  // picker's multi-account flow, the auto-configured toast + pending-restart
  // badge via an injected venue.seeded sys.event, and a full Alpaca
  // key-typed -> Test passes -> Save round trip. Those need either a live
  // OpenD login or WS-frame-level mocking of a specific command's response
  // (this file's only existing WS mock, error-matrix.spec.ts's
  // routeWebSocket, only intercepts connection lifecycle — open/close — not
  // individual command frames); building that harness is future work.
  // Unit coverage for all of the above already exists in
  // VenuesSection.test.tsx (all six moomoo states, the picker, Alpaca
  // two-slot save, the stale-draft reload guard).
  test("venues: the fixed broker-card roster renders, and a nonstandard legacy id is claimed by its slot", async ({ page }) => {
    await page.goto("/?workspace=e2e-settings-venues");
    await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });

    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Venues & creds", exact: true }).click();

    await expect(page.getByTestId("sim-card")).toBeVisible();
    await expect(page.getByTestId("moomoo-card")).toBeVisible();
    await expect(page.getByTestId("alpaca-card")).toBeVisible();
    await expect(page.getByTestId("tz-card")).toBeVisible();

    // e2e/serve.sh seeds config.toml with one venue, id "sim-paper" (a
    // nonstandard id, never renamed) — the Simulator card claims it by
    // broker alone, so it renders as a configured sim venue, not overflow.
    await expect(page.getByTestId("sim-startingbalance")).toBeVisible();
    await expect(page.getByTestId("other-venues")).toHaveCount(0);

    // No OpenD reachable in this replay-mode boot (venueseed itself isn't
    // even constructed on a -replay boot — see §A) — the moomoo card sits in
    // its deterministic pre-venue "waiting" state with no probe button.
    await expect(page.getByTestId("moomoo-body")).toContainText(/waiting for opend/i);
    await expect(page.getByTestId("moomoo-probe")).toHaveCount(0);

    // Client-side validation still blocks Save on a partially-typed key,
    // without any network round trip.
    await page.getByTestId("alpaca-paper-keyid").fill("only-id-typed");
    await expect(page.getByTestId("save-venues")).toBeDisabled();
  });
});
