import { test, expect } from "@playwright/test";

test("stock and crypto Following windows run independently", async ({ context, page }) => {
  const cryptoSymbol = "PERP.MEXC.BTC_USDT";
  let quoteFresh = true;
  await context.addInitScript(() => localStorage.setItem("etape.venueSetupHidden", "1"));
  await context.routeWebSocket("**/ws", (socket) => {
    const upstream = socket.connectToServer();
    // Keep demo startup from opening a Watchlist tab over Crypto List.
    upstream.onMessage(raw => {
      const packet = JSON.parse(raw.toString()) as { kind: string; topic?: string; payload?: object };
      socket.send(packet.kind === "snapshot" && packet.topic === "sys.session"
        ? JSON.stringify({ ...packet, payload: { ...packet.payload, mode: "live" } })
        : raw);
    });
    socket.onMessage(message => {
      const packet = JSON.parse(message.toString()) as { name?: string; corrId?: string };
      if (packet.name === "ListPerpetualMarkets") {
        socket.send(JSON.stringify({ kind: "ack", corrId: packet.corrId, status: "accepted", value: [
          { symbol: cryptoSymbol, exchange: "MEXC", base: "BTC", settlement: "USDT", tradingEnabled: true },
        ] }));
      } else if (packet.name === "GetCryptoScanner") {
        socket.send(JSON.stringify({ kind: "ack", corrId: packet.corrId, status: "accepted", value: {
          asOfMs: Date.now() - (quoteFresh ? 0 : 60_000),
          rows: [{ symbol: cryptoSymbol, last: 63000, reportedChangePct: 3, turnover: 10000000 }],
        } }));
      } else {
        upstream.send(message);
      }
    });
  });
  await page.goto("/?workspace=monitoring");
  await page.getByRole("button", { name: "Use this Scanner as Monitoring Source" }).click();
  await expect(page.getByRole("button", { name: "Disable Scanner Sync" })).toBeVisible();

  await page.getByRole("button", { name: /new window/i }).click();
  const [crypto] = await Promise.all([
    page.waitForEvent("popup"),
    page.getByRole("button", { name: "Crypto Monitoring", exact: true }).click(),
  ]);
  await expect(crypto.getByRole("table", { name: "MEXC Crypto List" })).toContainText("BTC/USDT");
  await crypto.getByRole("button", { name: "Enable Scanner Sync" }).click();
  await expect(crypto.getByTitle("Following 1/4")).toBeVisible();
  await crypto.getByRole("searchbox", { name: "Search USDT perpetuals" }).fill("ETH");
  await expect(crypto.getByText("No matching markets.")).toBeVisible();
  await expect(crypto.getByTitle("Following 1/4")).toBeVisible();
  quoteFresh = false;
  await expect(crypto.getByText("Waiting for fresh crypto quotes.")).toBeVisible();
  await expect(crypto.getByTestId("panel-symbol").filter({ hasText: "BTC" })).toHaveCount(1);
  await crypto.getByRole("searchbox", { name: "Search USDT perpetuals" }).clear();
  await expect(crypto.getByRole("row", { name: /BTC\/USDT/ }).getByRole("cell", { name: "—" })).toHaveCount(3);
  await crypto.getByRole("button", { name: "Disable Scanner Sync" }).click();
  await expect(crypto.getByRole("button", { name: "Enable Scanner Sync" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Disable Scanner Sync" })).toBeVisible();
});
