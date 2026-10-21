import { test, expect, type Page } from "@playwright/test";
import type { ClientMessage, PriceAlert, PriceAlertNotification } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION, type Workspace } from "../src/chrome/workspace";

async function trackLinks(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const target = window as unknown as { alertLinks: unknown[] };
    target.alertLinks = [];
    const NativeChannel = window.BroadcastChannel;
    window.BroadcastChannel = class extends NativeChannel {
      constructor(name: string) {
        super(name);
        if (name === "etape.link") this.addEventListener("message", (event) => target.alertLinks.push(event.data));
      }
      postMessage(message: unknown): void {
        if (this.name === "etape.link") target.alertLinks.push(message);
        super.postMessage(message);
      }
    };
  });
}

test("alert symbols prefer open panel colors and fall back to source after its window closes", async ({ page, context }, testInfo) => {
  const hostId = `e2e-alert-host-${Date.now()}`, otherId = `${hostId}-other`;
  const alert: PriceAlert = { id: "alert", symbol: "US.DRGO", sourceGroup: "blue", condition: "at-or-above", price: 5.5,
    label: "Breakout", sessions: { pre: true, rth: true, post: true }, state: "triggered", version: 1, createdMs: 1, updatedMs: 1 };
  const trigger = { ...alert, id: "trigger", alertId: alert.id, actualPrice: 5.6, session: "rth", tsMs: Date.now(), reason: "Target reached" };
  const senders = new Map<string, (notification: PriceAlertNotification) => void>();
  const commands: string[] = [];
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind !== "command") { server.send(raw); return; }
      commands.push(message.name);
      const key = (message.args as { key?: string } | undefined)?.key;
      if (message.name === "GetConfig" && (key === `workspace.${hostId}` || key === `workspace.${otherId}`)) {
        const name = key.slice("workspace.".length);
        const seed: Workspace = { name, layoutVersion: WORKSPACE_LAYOUT_VERSION,
          panels: [{ id: "alerts", panelId: "alerts", group: name === hostId ? null : "red", settings: {} }],
          layout: {}, groups: {} };
        senders.set(name, (notification) => socket.send(JSON.stringify({ kind: "delta", topic: "alerts.trigger", payload: notification })));
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: seed }));
      } else if (message.name === "ClaimPriceAlertNotification") {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: true }));
      } else server.send(raw);
    });
    server.onMessage((raw) => {
      if (typeof raw !== "string") { socket.send(raw); return; }
      const frame = JSON.parse(raw);
      socket.send(frame.topic === "alerts.state" ? JSON.stringify({ ...frame, payload: {
        alerts: [alert], history: [trigger], historyTotal: 1, revision: 1, error: "",
      } }) : raw);
    });
  });
  const other = await context.newPage();
  await Promise.all([trackLinks(page), trackLinks(other)]);
  await page.goto(`/?workspace=${hostId}`);
  await other.goto(`/?workspace=${otherId}`);
  await expect(page.getByRole("button", { name: "Load DRGO", exact: true })).toBeDisabled();
  await expect(other.getByRole("button", { name: "Load DRGO into red group" })).toBeVisible();
  await expect(page.getByTestId("chart-host")).toHaveCount(0);
  await expect(other.getByTestId("chart-host")).toHaveCount(0);

  let sequence = 0;
  const popup = async (color: string) => {
    const send = senders.get(hostId);
    if (!send) throw new Error("Popup workspace has no connection");
    send({ id: `burst-${++sequence}`, expiresMs: Date.now() + 5000, triggers: [trigger] });
    return await page.getByRole("button", { name: `Load DRGO into ${color} group` }).waitFor().then(
      () => page.getByRole("button", { name: `Load DRGO into ${color} group` }),
    );
  };
  const linkReceived = async (color: string) => expect.poll(() => page.evaluate((group) => {
    return (window as unknown as { alertLinks: Array<{ group?: string; symbol?: string }> }).alertLinks
      .some((entry) => entry.group === group && entry.symbol === "US.DRGO");
  }, color)).toBe(true);

  const first = await popup("red");
  await first.focus();
  await page.keyboard.press("Enter");
  await linkReceived("red");
  await expect.poll(() => other.evaluate(() => (window as unknown as { alertLinks: Array<{ group?: string; symbol?: string }> })
    .alertLinks.some((entry) => entry.group === "red" && entry.symbol === "US.DRGO"))).toBe(true);

  await other.locator(".ledger-header", { hasText: "Alerts" }).getByRole("button", { name: "link group" }).click();
  await other.getByRole("button", { name: "Yellow group", exact: true }).click();
  await expect(other.getByRole("button", { name: "Load DRGO into yellow group" })).toBeVisible();
  await (await popup("yellow")).click();
  await linkReceived("yellow");

  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("radio", { name: theme, exact: true }).check();
    await page.mouse.click(5, 5);
    await page.setViewportSize({ width: 520, height: 800 });
    const symbol = await popup("yellow");
    await symbol.focus();
    const bounds = await symbol.boundingBox();
    expect(bounds?.width).toBeGreaterThan(20);
    await page.screenshot({ path: testInfo.outputPath(`${theme.toLowerCase()}-alerts.png`) });
    await page.setViewportSize({ width: 1280, height: 900 });
  }

  await other.close();
  await page.getByRole("button", { name: "Load DRGO into blue group" }).click();
  await linkReceived("blue");
  await expect(page.getByTestId("chart-host")).toHaveCount(0);
  expect(commands.some((name) => /SubmitOrder|PlaceOrder|FocusMainWorkspace/.test(name))).toBe(false);
  await expect(page.getByRole("button", { name: "View chart", exact: true })).toHaveCount(0);
});
