import { expect, test } from "@playwright/test";
import { WORKSPACE_LAYOUT_VERSION } from "../src/chrome/workspace";
import type { AlpacaStockMargin, ClientMessage, QueryAlpacaStockMarginArgs } from "../src/wire/contract";

test("Stock Info ignores a late margin result for the previous symbol", async ({ page }) => {
  const workspace = "e2e-stock-info-margin";
  const now = Date.now();
  let replyToAapl: (() => void) | undefined;
  await page.routeWebSocket("**/ws", (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      const message = JSON.parse(raw.toString()) as ClientMessage;
      if (message.kind === "command" && message.name === "GetConfig" && (message.args as { key: string }).key === `workspace.${workspace}`) {
        socket.send(JSON.stringify({ kind: "ack", corrId: message.corrId, status: "accepted", value: {
          name: workspace, layoutVersion: WORKSPACE_LAYOUT_VERSION, layout: null,
          panels: [{ id: "margin", panelId: "stock-info", group: null, settings: { symbol: "US.AAPL", detailsCollapsed: false } }],
        } }));
      } else if (message.kind === "query" && message.name === "QueryAlpacaStockMargin") {
        const { symbol } = message.args as QueryAlpacaStockMarginArgs;
        const reply = (long: string) => {
          const payload: AlpacaStockMargin = { source: "live", long, short: null, fetchedAtMs: now, nextCheckMs: now + 900_000, stale: false };
          socket.send(JSON.stringify({ kind: "result", corrId: message.corrId, payload }));
        };
        if (symbol === "US.AAPL") {
          replyToAapl = () => {
            reply("30");
            // The visible news item confirms the browser processed a later frame.
            socket.send(JSON.stringify({ kind: "delta", topic: "news.item", payload: {
              id: "margin-test-news", symbols: ["US.NVDA"], headline: "NVDA fixture headline",
              url: "https://example.com/nvda", seen_at: new Date(now).toISOString(), published_precision: "unknown",
              type: "news", catalyst_score: 65, catalyst_category: "earnings",
            } }));
          };
        }
        else if (symbol === "US.NVDA") reply("75.25");
      } else server.send(raw);
    });
  });
  await page.goto(`/?workspace=${workspace}`);
  const onboarding = page.getByRole("button", { name: "I'll do it later", exact: true });
  if (await onboarding.isVisible()) await onboarding.click();
  await expect.poll(() => Boolean(replyToAapl)).toBe(true);
  await page.getByTestId("panel-symbol").click();
  await page.keyboard.type("NVDA");
  await page.keyboard.press("Enter");
  await expect(page.getByText("Alpaca Live margin", { exact: true })).toBeVisible();
  await expect(page.getByText("Long 75.25%", { exact: true })).toBeVisible();
  await expect(page.getByText("Short —", { exact: true })).toBeVisible();
  replyToAapl?.();
  await expect(page.getByText("NVDA fixture headline", { exact: true })).toBeVisible();
  await expect(page.getByText("Long 75.25%", { exact: true })).toBeVisible();
  await expect(page.getByText("Long 30%", { exact: true })).toHaveCount(0);
});
