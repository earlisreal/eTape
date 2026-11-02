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
  test("data settings save and reopen in the isolated demo config", async ({ page }) => {
    await page.goto("/?workspace=e2e-settings-data");
    await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Data & storage", exact: true }).click();
    const rth = page.getByLabel("Regular-session refresh (seconds)");
    const yahoo = page.getByLabel("Use experimental Yahoo headline supplement");
    await expect(rth).toHaveValue("1");
    await page.screenshot({ path: test.info().outputPath("data-settings.png"), animations: "disabled" });
    await rth.fill("2.5");
    await yahoo.check();
    await page.getByRole("button", { name: "Save data settings" }).click();
    await expect(page.getByText("Saved.", { exact: true })).toBeVisible();
    await expect(page.getByText(/Changes apply the next time live mode starts/)).toBeVisible();
    await page.getByRole("button", { name: "Close settings" }).click();
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("button", { name: "Data & storage", exact: true }).click();
    await expect(rth).toHaveValue("2.5");
    await expect(yahoo).toBeChecked();
    await rth.fill("1");
    await yahoo.uncheck();
    await page.getByRole("button", { name: "Save data settings" }).click();
    await expect(page.getByText("Saved.", { exact: true })).toBeVisible();
  });

  test("orders: a template's size edit round-trips and changes the fired order qty", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-settings-orders", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("boot-status-banner")).toHaveCount(0, { timeout: 15_000 });
    await page.getByTestId("venue").selectOption("sim-paper");

    // A preset starts without a focused symbol. Set a demo-feed symbol so the
    // linked ticket and hotkey both have a real quote.
    const domHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
    const domPanel = page.getByRole("region", { name: "ladder", exact: true });
    await domPanel.getByTestId("panel-body").click();
    await expect(domPanel.locator(".panel-focused")).toHaveCount(1);
    await page.keyboard.type("ECLP");
    await page.keyboard.press("Enter");
    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("ECLP", { timeout: 10_000 });
    await page.keyboard.type("DRGO");
    await page.keyboard.press("Enter");
    await expect(domHeader.getByTestId("panel-symbol")).toHaveText("DRGO", { timeout: 10_000 });
    await expect(page.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "ready");

    // Arm the master switch (same as smoke.spec's paper-fill test) — the hotkey
    // engine (useHotkeys.ts) refuses to fire while disarmed. Per-venue arm was
    // removed; master arm + the risk-limit gate are the only checks now.
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("LOCK TRADING");

    // eTape ships with NO default order templates/hotkeys (DEFAULT_TEMPLATES in
    // actionTemplate.ts is empty) — build one from scratch: a Dollar/$5000 Buy
    // at live Ask with zero offset, bound to Ctrl+1. On an empty template list
    // uid() deterministically mints "tmpl-1-1" for the first add.
    await page.getByTestId("open-settings").click();
    await page.getByTestId("add-template").click();
    await page.getByTestId("add-place").click();
    await page.getByLabel("size-mode-tmpl-1-1").selectOption("Dollar");
    await page.getByLabel("size-value-tmpl-1-1", { exact: true }).fill("5000");
    await page.getByTestId("tmpl-hotkey-tmpl-1-1").click();
    await page.keyboard.press("Control+1");
    await expect(page.getByTestId("tmpl-hotkey-tmpl-1-1")).toHaveValue("Ctrl+1");
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5); // click the modal backdrop to close (SettingsModal has no Escape handler)

    // Wait for a positive quote before firing the simulated order.
    const askLocator = page.getByTestId("ask");
    await expect.poll(async () => Number(await askLocator.innerText()), { timeout: 15_000 }).toBeGreaterThan(0);
    const buyToast = page.getByRole("alert").filter({ hasText: /^BUY [\d,]+ DRGO @/ });
    const sizeAndPrice = (text: string) => {
      const match = /^BUY ([\d,]+) DRGO @ ([\d.]+)/.exec(text);
      if (!match) throw new Error(`Unexpected order toast: ${text}`);
      return { qty: Number(match[1].replaceAll(",", "")), price: Number(match[2]) };
    };

    // Fire the newly-bound template and capture its flash toast (pushed by
    // OrderCommands.submit in commands.ts, rendered as a role="alert" Toast).
    await page.locator("body").click(); // ensure document focus; useHotkeys bails if !document.hasFocus()
    await page.keyboard.press("Control+1");
    await expect(buyToast).toBeVisible({ timeout: 10_000 });
    const first = sizeAndPrice(await buyToast.innerText());
    expect(first.qty * first.price).toBeGreaterThan(4980);
    expect(first.qty * first.price).toBeLessThanOrEqual(5020);
    await buyToast.click(); // dismiss before the second order's toast

    // Edit the template's dollar amount via the order ticket's own gear, which
    // (OpenSettingsContext.openOrderSettings) jumps straight to the "Orders &
    // hotkeys" settings section — no need to navigate the modal's nav tabs.
    await page.getByTestId("open-settings").click();
    await page.getByLabel("size-value-tmpl-1-1", { exact: true }).fill("7000");
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5); // click the modal backdrop to close (SettingsModal has no Escape handler)

    // Re-fire the same hotkey binding; the qty must now reflect the edited
    // $7000, not the original $5000 — this is the actual round-trip: settings
    // UI -> SetConfig("orderConfig") -> shared OrderConfigProvider context ->
    // useHotkeys reads the same config -> resolvePlaceTemplate recomputes size.
    await page.keyboard.press("Control+1");
    await expect(buyToast).toBeVisible({ timeout: 10_000 });
    const second = sizeAndPrice(await buyToast.innerText());
    expect(second.qty).toBeGreaterThan(first.qty);
    expect(second.qty * second.price).toBeGreaterThan(6980);
    expect(second.qty * second.price).toBeLessThanOrEqual(7020);

    // Cleanup: the orderConfig key and arm state are engine-side (shared across
    // every spec file in one `npm run e2e` run, since webServer boots ONE engine
    // for the whole invocation). reset-defaults now restores the blank baseline
    // (no default templates/hotkeys) — the tmpl-1-1 template built above must
    // not survive into later specs. Restore both this and arm state so
    // smoke.spec.ts's arm test (which expects the chip to transition from
    // "UNLOCK TRADING" (disarmed) to "LOCK TRADING" (armed))
    // and any hotkey-dependent test that runs after this file aren't left
    // holding a stray template or an already-armed gate.
    await page.getByTestId("open-settings").click();
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("reset-defaults").click();
    await page.getByTestId("save").click();
    await page.mouse.click(5, 5);
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("UNLOCK TRADING");
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

    // The demo health feed reports a synthetic connection, but no moomoo venue
    // exists and the live auto-config seeder is not started.
    await expect(page.getByTestId("moomoo-body")).toContainText(/waiting for opend/i);
    await expect(page.getByTestId("moomoo-probe")).toBeVisible();

    // Client-side validation still blocks Save on a partially-typed key,
    // without any network round trip.
    await page.getByTestId("alpaca-paper-keyid").fill("only-id-typed");
    await expect(page.getByTestId("save-venues")).toBeDisabled();
  });
});
