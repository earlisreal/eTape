import { test, expect } from "@playwright/test";

test("stock and crypto Following windows run independently", async ({ context, page }) => {
  const cryptoSymbol = "PERP.MEXC.BTC_USDT";
  await context.routeWebSocket("**/ws", (socket) => {
    const upstream = socket.connectToServer();
    socket.onMessage(message => {
      const packet = JSON.parse(message.toString()) as { name?: string; corrId?: string };
      if (packet.name === "GetCryptoScanner") {
        socket.send(JSON.stringify({ kind: "ack", corrId: packet.corrId, status: "accepted", value: {
          asOfMs: Date.now(), warming: false,
          rows: [{ symbol: cryptoSymbol, base: "BTC", last: 63000, changePct: 3, turnover: 10000000, spreadBps: 2 }],
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
  await expect(crypto.getByRole("table", { name: "MEXC crypto Scanner results" })).toContainText("BTC/USDT");
  await crypto.getByRole("button", { name: "Enable Scanner Sync" }).click();
  await expect(crypto.getByTitle("Following 1/4")).toBeVisible();
  await crypto.getByRole("button", { name: "Disable Scanner Sync" }).click();
  await expect(crypto.getByRole("button", { name: "Enable Scanner Sync" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Disable Scanner Sync" })).toBeVisible();
});
