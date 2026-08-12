import { test, expect, type Page } from "@playwright/test";

async function gotoMainTrading(page: Page): Promise<void> {
  await page.goto("/?workspace=main");
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
}

async function seedHotkey(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Orders & hotkeys", exact: true }).click();
  await page.getByTestId("add-template").click();
  await page.getByTestId("add-place").click();
  await page.getByLabel("size-mode-tmpl-1-1").selectOption("Dollar");
  await page.getByTestId("size-value-tmpl-1-1").fill("5000");
  await page.getByTestId("tmpl-hotkey-tmpl-1-1").click();
  await page.keyboard.press("Control+1");
  await page.getByTestId("save").click();
  await page.mouse.click(5, 5);
}

test.describe("ticketless cross-window hotkeys", () => {
  test("uses the last linked panel target, then blocks ungrouped and closed owners", async ({ page, context }) => {
    await page.addInitScript(() => { Date.now = () => 1_783_522_800_000; });
    const runId = Math.random().toString(36).slice(2);
    const otherWorkspace = `e2e-ticketless-other-${runId}`;
    await gotoMainTrading(page);

    // Remove the ticket before configuring or firing the hotkey. The linked
    // DOM panel remains the only execution context used by this test.
    const ticket = page.locator(".ledger-header", { hasText: "Order Ticket" });
    await ticket.getByRole("button", { name: "close panel" }).click();
    await expect(ticket).toHaveCount(0);

    // Establish a real blue-group focus, ending on DRGO so the deterministic
    // demo feed has a quote to validate and submit.
    const domPanel = page.locator(".ledger-header", { hasText: "DOM Ladder" }).locator("xpath=..");
    await domPanel.getByTestId("panel-body").click();
    await expect(domPanel).toHaveClass(/panel-focused/);
    await page.keyboard.type("ECLP");
    await page.keyboard.press("Enter");
    await expect(domPanel.getByTestId("panel-symbol")).toHaveText("ECLP");
    await page.keyboard.type("DRGO");
    await page.keyboard.press("Enter");
    await expect(domPanel.getByTestId("panel-symbol")).toHaveText("DRGO");
    await expect(page.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "ready");
    await expect(page.getByTestId("hotkey-target-cue")).toHaveAccessibleName(/DRGO.*sim-paper/i);
    await page.waitForTimeout(5_000); // allow the demo feed to publish the first quote

    await seedHotkey(page);
    await page.getByTestId("arm-chip").click();
    await expect(page.getByTestId("arm-chip")).toHaveText("LOCK TRADING");

    // A newly opened blank workspace requests/replays the target. Bringing it
    // frontmost supplies OS focus for the keyboard path but does not activate
    // any Dockview panel in that window.
    const other = await context.newPage();
    await other.goto(`/?workspace=${otherWorkspace}`);
    await expect(other.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
    await other.bringToFront();
    await expect(other.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "ready");
    await expect(other.getByTestId("hotkey-target-cue")).toHaveAccessibleName(/DRGO.*sim-paper/i);
    await other.keyboard.press("Control+1");
    await expect(other.getByRole("alert")).toContainText(/BUY .* DRGO @/i, { timeout: 10_000 });
    await expect(page.getByTestId("pos-row-sim-paper-US.DRGO")).toBeVisible({ timeout: 10_000 });

    // A user activation of an ungrouped panel becomes the coordinated target,
    // but scoped place actions must consume and block it.
    await page.bringToFront();
    await page.getByRole("button", { name: "+ Add panel" }).click();
    await page.locator(".popover").getByText("Stock Info", { exact: true }).click();
    const stockPanel = page.getByRole("tabpanel", { name: "Stock Info" });
    await page.getByRole("tab", { name: "News", exact: true }).click();
    await page.getByRole("tab", { name: "Stock Info", exact: true }).last().click();
    await stockPanel.getByTestId("panel-body").click();
    await expect(page.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "ungrouped");
    await other.bringToFront();
    await expect(other.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "ungrouped");
    await other.keyboard.press("Control+1");
    await expect(other.getByText(/hotkey blocked — target panel is ungrouped/i)).toBeVisible();

    // Closing the owner clears only its own target. The remaining window sees
    // no target and blocks the next scoped binding before SubmitOrder.
    await page.close();
    await expect(other.getByTestId("hotkey-target-cue")).toHaveAttribute("data-state", "no-target");
    await other.keyboard.press("Control+1");
    await expect(other.getByText(/hotkey blocked — no grouped panel target/i)).toBeVisible();
    await other.close();
  });
});
