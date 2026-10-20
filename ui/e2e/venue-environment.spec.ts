import { test, expect } from "@playwright/test";
import type { ClientMessage, ExecStatus } from "../src/wire/contract";
import { WORKSPACE_LAYOUT_VERSION, type Workspace } from "../src/chrome/workspace";

test("venue badges remain visible across themes, narrow headers, and workspace targets", async ({ page, context }, testInfo) => {
  let environment = "live";
  const publishers: Array<() => void> = [];
  const longVenue = "a-very-long-venue-name-paper";
  const workspace = `e2e-venue-badge-${Date.now()}`;
  const seed: Workspace = {
    name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION,
    panels: [
      { id: "ticket", panelId: "order-ticket", group: "blue", settings: {} },
      { id: "account", panelId: "account", group: "blue", settings: {} },
    ],
    groups: { blue: "US.DRGO" }, linkVenues: { blue: "sim-paper" },
    layout: {
      grid: { root: { type: "branch", data: [
        { type: "leaf", data: { id: "ticket", views: ["ticket"], activeView: "ticket" }, size: 640 },
        { type: "leaf", data: { id: "account", views: ["account"], activeView: "account" }, size: 640 },
      ], size: 800 }, width: 1280, height: 800, orientation: "HORIZONTAL" },
      panels: {
        ticket: { id: "ticket", contentComponent: "ticket", title: "order-ticket" },
        account: { id: "account", contentComponent: "account", title: "account" },
      }, activeGroup: "ticket",
    },
  };
  // Only display metadata is changed. The isolated demo engine remains sim-only.
  await context.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: seed }));
      } else server.send(raw);
    });
    server.onMessage((message) => {
      if (typeof message !== "string") { socket.send(message); return; }
      const frame = JSON.parse(message);
      if (frame.topic !== "exec.status") { socket.send(message); return; }
      const status: ExecStatus = frame.payload;
      const publish = () => socket.send(JSON.stringify({ ...frame, payload: {
        ...status, masterArmed: false, venues: [
          ...status.venues.map((v) => ({ ...v, env: environment, connected: false })),
          { ...status.venues[0], venue: longVenue, env: "live", connected: false },
        ],
      } }));
      publishers.push(publish);
      publish();
    });
  });
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("acct-equity")).toBeVisible();
  await page.getByRole("tab", { name: "Watchlist", exact: true }).getByRole("button", { name: "Close tab" }).click();
  const ticket = page.locator(".ledger-header", { hasText: "Order Ticket" });
  const account = page.locator(".ledger-header", { hasText: "Account" });
  await page.getByTestId("venue").selectOption("sim-paper");
  await ticket.click();
  const cue = page.getByTestId("hotkey-target-cue");
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await expect(ticket.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await expect(account.getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await page.getByTestId("venue").selectOption(longVenue);
  const other = await context.newPage();
  await other.goto(`/?workspace=e2e-venue-badge-${Date.now()}`);
  await expect(other.getByTestId("hotkey-target-cue").getByTestId("venue-environment-badge")).toHaveText("LIVE");
  await page.bringToFront();

  for (const theme of ["Light", "Dark"]) {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.getByRole("radio", { name: theme, exact: true }).check();
    await page.mouse.click(5, 5);
    for (const width of [1280, 900]) {
      await page.setViewportSize({ width, height: 900 });
      for (const host of [page.getByTestId("top-bar"), ticket, account]) {
        const badge = host.getByTestId("venue-environment-badge");
        await expect(badge).toBeVisible();
        const bounds = await badge.evaluate((el) => {
          const rect = el.getBoundingClientRect();
          const host = el.closest(".ledger-header, .top-bar")!.getBoundingClientRect();
          return { left: rect.left, right: rect.right, hostLeft: host.left, hostRight: host.right, width: rect.width,
            covered: !el.contains(document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)) };
        });
        expect(bounds.width).toBeGreaterThan(20);
        expect(bounds.left).toBeGreaterThanOrEqual(bounds.hostLeft);
        expect(bounds.right).toBeLessThanOrEqual(bounds.hostRight);
        expect(bounds.covered).toBe(false);
      }
      await page.screenshot({ path: testInfo.outputPath(`${theme.toLowerCase()}-${width}.png`) });
    }
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByTestId("venue").selectOption("sim-paper");
  environment = "paper";
  publishers.forEach((publish) => publish());
  await expect(cue.getByTestId("venue-environment-badge")).toHaveText("SIM");
  await expect(other.getByTestId("hotkey-target-cue").getByTestId("venue-environment-badge")).toHaveText("SIM");
  await other.close();
});
