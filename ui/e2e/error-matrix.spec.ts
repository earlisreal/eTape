import { test, expect, type Page } from "@playwright/test";

// context.setOffline(true) does not close an already-open localhost WebSocket in
// Chromium (verified: no onclose fires, no "reconnecting" state) — it only blocks
// *new* network requests. To actually exercise WsClient's onclose -> reconnecting
// path we route the WS ourselves and force-close the page-side connection with
// WebSocketRoute.close(); the reconnect attempt opens a fresh WebSocket that the
// same route re-intercepts and passes through to the real engine.
type WebSocketRoute = Parameters<Parameters<Page["routeWebSocket"]>[1]>[0];

// Blank-start workspace model (Task 7/10): `?workspace=<name>` always starts
// empty; reaching a populated layout means clicking a preset card in the
// empty-state Catalog. Each test below uses its own workspace name so this
// file's runs stay isolated from smoke.spec.ts and from each other.
async function gotoAndApplyPreset(page: Page, workspace: string, presetName: "Trading" | "Monitoring"): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: new RegExp(`^${presetName}`) }).click();
}

test.describe("error-handling matrix", () => {
  test("WS drop shows the reconnect overlay, recovery clears it", async ({ page }) => {
    let current: WebSocketRoute | null = null;
    await page.routeWebSocket("**/ws", (ws) => {
      current = ws;
      ws.connectToServer();
    });

    await gotoAndApplyPreset(page, "e2e-err-ws-drop", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    await current?.close();
    await expect(page.getByText(/reconnect|disconnected|connecting/i).first()).toBeVisible({ timeout: 10_000 });

    // WsClient's jittered backoff (~0.5-1s on the first attempt) reopens a new
    // WebSocket, which the route above re-intercepts and passes through.
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(/reconnect|disconnected|connecting/i)).toHaveCount(0);
  });

  test("engine unreachable at load stays in a connecting/error state", async ({ page }) => {
    // page.route("**/ws", abort) does NOT intercept the WebSocket upgrade at all in
    // this Playwright/Chromium combo (verified: the real WS still connects normally
    // through an "aborted" route). Naively using routeWebSocket without
    // connectToServer() also doesn't reproduce "unreachable": Playwright mocks an
    // immediate open() for unconnected routes ("Playwright assumes that WebSocket
    // will be mocked, and opens the WebSocket inside the page automatically"),
    // which lands WsClient in "open" with no data, not a visible reconnect state.
    // Force-closing the mocked socket instead of opening it reproduces a real
    // engine-down handshake failure: WsClient never reaches "open" and its backoff
    // loop keeps re-hitting this same route, which keeps closing it — the
    // reconnecting overlay stays up indefinitely, exactly like a dead engine.
    await page.routeWebSocket("**/ws", (ws) => {
      void ws.close({ code: 1006, reason: "e2e: engine unreachable" });
    });
    // Never reaches the empty-state Catalog (WorkspaceStore.load's GetConfig
    // never round-trips), so no preset click here — just navigate.
    await page.goto("/?workspace=e2e-err-unreachable");
    await expect(page.getByText(/reconnect|connecting|disconnected/i).first()).toBeVisible({ timeout: 15_000 });
  });

  test("submitting a MARKET order while disarmed surfaces the gate block", async ({ page }) => {
    // preChecks.ts coerces MARKET->LIMIT-at-last outside real-wall-clock RTH (a
    // genuine client-side safety feature, independent of the replay day's
    // simulated clock). An un-pinned clock would get the order silently rewritten
    // to an unmarketable LIMIT before it ever reaches the wire, so this test would
    // never see the real engine gate block. Pin to a weekday RTH instant so the
    // MARKET order clears preCheck as MARKET and actually reaches the gate.
    // Wed 2026-07-08T15:00:00Z = 11:00 ET (see smoke.spec.ts for the same pin).
    await page.addInitScript(() => { Date.now = () => 1_783_522_800_000; });
    await gotoAndApplyPreset(page, "e2e-err-gate-block", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // Do NOT arm. MARKET so it clears client pre-checks and reaches the engine gate.
    await page.getByTestId("order-type").selectOption("MARKET");
    await page.getByTestId("side-BUY").click();
    await expect(page.getByText(/blocked|disarm|master/i).first()).toBeVisible({ timeout: 10_000 });
  });

  // NOTE: StreamGap (outbound-queue overflow → forced re-snapshot → the StreamGap
  // badge) is not deterministically triggerable from the browser and is not
  // automated here. It retains unit coverage (WsClient re-snapshot on gap; the
  // OpenOrders StreamGap badge render). Verify manually if the badge changes.
});
