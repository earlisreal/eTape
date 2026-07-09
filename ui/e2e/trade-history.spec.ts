import { test, expect, type Page, type Locator } from "@playwright/test";

// Task 11 (plan B7): the one verification layer unit/component tests can't
// provide for the Trade History feature (Tasks 1-10) — a real browser, the
// real WebSocket wire, and the real (simulated) broker fill path, end to end.
//
// This file runs LAST alphabetically in the shared `npm run e2e` invocation
// (error-matrix -> settings-redesign -> smoke -> trade-history), against the
// SAME long-lived replay engine process the other three files already used.
// Two things follow from that:
//   1. smoke.spec.ts buys 100 US.AAPL and never sells; settings-redesign.spec.ts
//      also trades US.AAPL (via hotkeys). This file trades US.NVDA exclusively
//      to avoid inheriting any position/order state from those runs (NVDA and
//      AAPL are the only two symbols genjournal writes into the synthetic
//      replay day, so NVDA is the only alternative available).
//   2. smoke.spec.ts's paper-order test arms the master chip and never
//      disarms; settings-redesign.spec.ts's own arm test ends by disarming
//      it. Whichever ran most recently determines the arm state this file
//      inherits — never assume either state; check first (see ensureArmed
//      below).
//
// -replay-hold means the engine keeps serving the LAST replayed mark forever
// once the synthetic day finishes, so by the time this file runs the mark has
// already drifted from NVDA's $140.00 open. Never hardcode an assumed mark —
// read the ticket's live bid/ask at test time and compute order prices/
// expected realized P&L from those same read values (mirrors the convention
// settings-redesign.spec.ts already uses for the live `ask` readout).
//
// Fill mechanics (engine/internal/broker/sim/sim.go): MARKET fills at the
// frozen mark, which would make every fill on a symbol land at the same
// price and realized P&L always exactly 0 — useless for this test. LIMIT
// orders fill at their OWN limit price as long as they're "marketable"
// against the mark (BUY/COVER limit >= mark; SELL/SHORT limit <= mark).
//
// Discovery #1 (deviates from the task brief's assumption): the ticket's
// bid/ask readout does NOT track the mark that governs marketability here.
// genjournal (engine/cmd/genjournal/main.go) writes exactly one
// feed.QuoteEvent + one feed.BookEvent per symbol at session open, then only
// feed.TicksEvent for the rest of the day — so the book (and the top-of-book
// bid/ask the ticket displays) stays frozen at the open price for the whole
// replay, while the broker's "mark" (engine/internal/md: applyTicks ->
// c.mark(...) on every accepted tick, wired to every sim broker's SetMark in
// cmd/etape/main.go's markBridge) drifts upward with the ticks: 20 bars x 6
// ticks/min x a net +0.27/min drift = +5.40 by the last bar. A +/-5 offset
// off the (stale) bid/ask is therefore NOT reliably marketable for BUY/COVER
// once the mark has drifted past it — verified empirically (a first pass at
// this file with a +/-5 offset left the BUY order resting at "Accepted",
// never filling). +/-20 comfortably clears the known +5.40 max drift with
// real margin, while keeping qty x price two orders of magnitude under the
// $100k gate limit (existing specs already submit ~$19k orders fine; this
// file's orders are ~$1.2k-2.4k). This is still a live-read offset from the
// ticket's bid/ask, per the brief's rule against hardcoding an absolute mark
// — just widened to account for the bid/ask feed's staleness relative to the
// tick-driven mark.
const MARKETABLE_OFFSET = 20;

// Discovery #2: closed-trade history (like positions/orders) is engine-side
// state keyed by (venue, symbol) — NOT scoped to a UI workspace. Each test
// below applies the Trading preset onto its OWN `?workspace=` name (so the
// two tests never share dockview layout/config), but since BOTH tests trade
// US.NVDA on sim-paper within the SAME shared engine process (workers: 1,
// fullyParallel: false — the whole file runs against one engine), the round
// trip closed by the FIRST test is still sitting in the TradeStore/broker
// when the SECOND test starts. A first pass at this file asserted an
// absolute "N closed trades" count per test and failed the second test: it
// actually saw 3 rows (1 leftover from the first test + 2 of its own), and a
// naive qty-based row filter mismatched because the frozen bid/ask (per
// Discovery #1) makes both tests compute IDENTICAL prices, so the leftover
// row and the new "long" row were indistinguishable by value alone. Fix:
// read a BASELINE (existing row testids + the footer's current realized
// total) before submitting any of this test's own orders, then identify
// "this test's rows" as whatever testids appear that weren't in the
// baseline, and assert the footer against baseline-plus-this-test's-realized
// rather than an absolute total. This also makes both tests robust to
// running in either order (or being retried), not just to today's ordering.
const money = (n: number): string => (n < 0 ? "−$" : "$") + Math.abs(n).toFixed(2);
function parseMoney(s: string): number {
  const negative = s.startsWith("−"); // unicode minus, per money()'s sign convention
  const magnitude = Number(s.replace(/^−/, "").replace("$", ""));
  return negative ? -magnitude : magnitude;
}

async function gotoAndApplyPreset(page: Page, workspace: string, presetName: "Trading" | "Monitoring"): Promise<void> {
  await page.goto(`/?workspace=${workspace}`);
  await expect(page.getByTestId("latency-readout")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: new RegExp(`^${presetName}`) }).click();
}

// Defensive arm check (do NOT blindly click — an earlier spec file in the
// same shared-engine run may have already armed the master switch; the arm
// chip TOGGLES, so an unconditional click could disarm an already-armed
// control instead of arming it).
async function ensureArmed(page: Page): Promise<void> {
  const armChip = page.getByTestId("arm-chip");
  await expect(armChip).toBeVisible({ timeout: 15_000 });
  if ((await armChip.innerText()).trim() !== "ARMED") {
    await armChip.click();
    await expect(armChip).toHaveText("ARMED");
  }
}

// Type-to-load NVDA into the DOM Ladder panel's header, same mechanism
// smoke.spec.ts's "link groups" test and settings-redesign.spec.ts already
// use: click the panel body to make it dockview's active panel, then type +
// Enter to commit. t-dom is in the Trading preset's "blue" group alongside
// t-ticket (the order ticket), so committing here moves the ticket's symbol
// too via LinkGroups.subscribe — no separate mechanism needed for the ticket.
async function focusSymbol(page: Page, symbol: string): Promise<void> {
  const domHeader = page.locator(".ledger-header", { hasText: "DOM Ladder" });
  const domPanel = domHeader.locator("xpath=..");
  await domPanel.getByTestId("panel-body").click();
  await expect(domPanel).toHaveClass(/panel-focused/);
  await page.keyboard.type(symbol);
  await page.keyboard.press("Enter");
  await expect(domHeader.getByTestId("panel-symbol")).toHaveText(symbol, { timeout: 10_000 });
}

async function readQuote(page: Page, testid: "bid" | "ask"): Promise<number> {
  const loc = page.getByTestId(testid);
  await expect(loc).not.toHaveText("—", { timeout: 15_000 }); // em dash placeholder for "no quote"
  const n = Number((await loc.innerText()).trim());
  expect(n).toBeGreaterThan(0);
  return n;
}

// Submits a LIMIT order at an exact price. The price string filled into the
// ticket is derived with the SAME toFixed(3) rounding used to compute the
// caller's `price` argument in the first place, so the value the engine
// receives (and fills at, for a marketable limit) exactly matches what the
// test asserts against later — no independent rounding step to drift out of
// sync.
async function submitLimit(page: Page, sideTestId: `side-${"BUY" | "SELL" | "SHORT" | "COVER"}`, price: number, qty: number): Promise<void> {
  await page.getByTestId("price").fill(price.toFixed(3));
  await page.getByTestId("amount").fill(String(qty));
  await page.getByTestId(sideTestId).click();
}

const tradeRowsLocator = (page: Page): Locator => page.locator('[data-testid^="trade-"]');
const tradeHistoryTab = (page: Page): Locator => page.getByRole("button", { name: "Trade History" });

async function tradeRowTestIds(page: Page): Promise<string[]> {
  return tradeRowsLocator(page).evaluateAll((els) => els.map((el) => el.getAttribute("data-testid") ?? ""));
}

async function waitForTotalTradeCount(page: Page, n: number): Promise<void> {
  const label = `${n} closed trade${n === 1 ? "" : "s"}`;
  await expect(page.getByText(label, { exact: true })).toBeVisible({ timeout: 10_000 });
}

interface TradeRow { symbol: string; venue: string; qty: number; entry: number; exit: number; realized: number }

async function readRowByTestId(page: Page, testId: string): Promise<TradeRow> {
  const cells = page.locator(`[data-testid="${testId}"]`).locator("td");
  return {
    symbol: (await cells.nth(0).innerText()).trim(),
    venue: (await cells.nth(1).innerText()).trim(),
    qty: Number((await cells.nth(2).innerText()).replace(/,/g, "")),
    entry: Number(await cells.nth(3).innerText()),
    exit: Number(await cells.nth(4).innerText()),
    realized: Number(await cells.nth(5).innerText()),
  };
}

test.describe("trade history", () => {
  test("a closed round trip produces one trade row with live-priced realized P&L", async ({ page }) => {
    await gotoAndApplyPreset(page, "e2e-trade-history-roundtrip", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await ensureArmed(page);
    await focusSymbol(page, "NVDA");

    await expect(page.getByTestId("order-type")).toHaveValue("LIMIT"); // ticket's own default; asserted, not relied on blindly

    // Order 1: BUY 10 @ ask+MARKETABLE_OFFSET (comfortably marketable; see
    // Discovery #1 above) opens the long.
    const ask1 = await readQuote(page, "ask");
    const buyPrice = Number((ask1 + MARKETABLE_OFFSET).toFixed(3));
    await submitLimit(page, "side-BUY", buyPrice, 10);
    // Positions tab is the Account panel's default; wait for the live NVDA
    // position (not a generic "Filled" text match, which could also match an
    // unrelated AAPL order elsewhere in the same venue-scoped Orders table).
    await expect(page.getByTestId("flatten-sim-paper-US.NVDA")).toBeVisible({ timeout: 10_000 });

    // Baseline, taken after the opening leg (which never emits a trade on
    // its own — RoundTripAggregator's first-fill-from-flat rule) but before
    // the closing leg. See Discovery #2 above for why this matters.
    await tradeHistoryTab(page).click();
    const baselineIds = await tradeRowTestIds(page);
    const baselineRealized = parseMoney(await page.getByTestId("trades-day-realized").innerText());

    // Order 2: SELL 10 @ bid-MARKETABLE_OFFSET — same qty fully flattens the
    // position. Re-read bid rather than reusing a stale value (cheap to guard
    // even though the bid readout itself never moves under -replay-hold).
    const bid2 = await readQuote(page, "bid");
    const sellPrice = Number((bid2 - MARKETABLE_OFFSET).toFixed(3));
    await submitLimit(page, "side-SELL", sellPrice, 10);
    await waitForTotalTradeCount(page, baselineIds.length + 1);

    const afterIds = await tradeRowTestIds(page);
    const newIds = afterIds.filter((id) => !baselineIds.includes(id));
    expect(newIds, `expected exactly 1 new row; all ids: ${JSON.stringify(afterIds)}`).toHaveLength(1);
    const row = await readRowByTestId(page, newIds[0]);
    const expectedRealized = (sellPrice - buyPrice) * 10;

    expect(row.symbol).toBe("NVDA");
    expect(row.venue).toBe("sim-paper");
    expect(row.qty).toBe(10);
    expect(row.entry).toBeCloseTo(buyPrice, 2);
    expect(row.exit).toBeCloseTo(sellPrice, 2);
    expect(row.realized).toBeCloseTo(expectedRealized, 2);

    await expect(page.getByTestId("trades-day-realized")).toHaveText(money(baselineRealized + expectedRealized));
  });

  test("a flip (3 orders) produces two closed trades with correctly chained entry/exit", async ({ page }) => {
    test.setTimeout(45_000); // three sequential order round trips; give more headroom than the 30s default
    await gotoAndApplyPreset(page, "e2e-trade-history-flip", "Trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await ensureArmed(page);
    await focusSymbol(page, "NVDA");

    // Order 1: BUY 10 @ p1 = ask+MARKETABLE_OFFSET -> opens long 10.
    const ask1 = await readQuote(page, "ask");
    const p1 = Number((ask1 + MARKETABLE_OFFSET).toFixed(3));
    await submitLimit(page, "side-BUY", p1, 10);
    await expect(page.getByTestId("flatten-sim-paper-US.NVDA")).toBeVisible({ timeout: 10_000 });

    // Baseline (see Discovery #2): the previous test in this file closes its
    // own NVDA round trip on the same shared engine, so this test may not be
    // starting from zero closed trades — read whatever's already there
    // before this test's own closing/flip fills land.
    await tradeHistoryTab(page).click();
    const baselineIds = await tradeRowTestIds(page);
    const baselineRealized = parseMoney(await page.getByTestId("trades-day-realized").innerText());

    // Order 2: SELL 15 @ p2 = bid-MARKETABLE_OFFSET -> the flip. Closes the
    // long 10 (trade #1: entry=p1, exit=p2) and opens a NEW short 5 @ p2 in
    // the same fill
    // (engine/internal/exec/roundtrip_test.go::TestRoundTripLongToShortFlipInOneFill).
    const bid2 = await readQuote(page, "bid");
    const p2 = Number((bid2 - MARKETABLE_OFFSET).toFixed(3));
    await submitLimit(page, "side-SELL", p2, 15);
    await waitForTotalTradeCount(page, baselineIds.length + 1);

    // Order 3: COVER 5 @ p3 = ask+MARKETABLE_OFFSET (re-read; harmless even
    // though the ask readout itself never moves under -replay-hold) -> closes
    // the flip's short remainder (trade #2: entry=p2, the FLIP price per
    // roundtrip.go, exit=p3).
    const ask3 = await readQuote(page, "ask");
    const p3 = Number((ask3 + MARKETABLE_OFFSET).toFixed(3));
    await submitLimit(page, "side-COVER", p3, 5);
    await waitForTotalTradeCount(page, baselineIds.length + 2);

    const afterIds = await tradeRowTestIds(page);
    const newIds = afterIds.filter((id) => !baselineIds.includes(id));
    expect(newIds, `expected exactly 2 new rows; all ids: ${JSON.stringify(afterIds)}`).toHaveLength(2);
    const newRows = await Promise.all(newIds.map((id) => readRowByTestId(page, id)));

    const long = newRows.find((r) => r.qty === 10);
    const short = newRows.find((r) => r.qty === 5);
    expect(long, `expected a qty=10 row among this test's new rows: ${JSON.stringify(newRows)}`).toBeDefined();
    expect(short, `expected a qty=5 row among this test's new rows: ${JSON.stringify(newRows)}`).toBeDefined();

    // Long realized = (exit - entry) x qty; short realized is the mirror image
    // (you profit when price FALLS) = (entry - exit) x qty — confirmed against
    // roundtrip_test.go's own worked example (short opened @105, covered @110
    // -> realized -25, i.e. (105-110)*5, not (110-105)*5). A first pass at this
    // test used the long-side formula for both legs and got the short leg's
    // sign backwards (asserted +200.10, engine correctly reported -200.10).
    const expectedLongRealized = (p2 - p1) * 10;
    const expectedShortRealized = (p2 - p3) * 5;

    expect(long!.symbol).toBe("NVDA");
    expect(long!.venue).toBe("sim-paper");
    expect(long!.entry).toBeCloseTo(p1, 2);
    expect(long!.exit).toBeCloseTo(p2, 2);
    expect(long!.realized).toBeCloseTo(expectedLongRealized, 2);

    expect(short!.symbol).toBe("NVDA");
    expect(short!.venue).toBe("sim-paper");
    expect(short!.entry).toBeCloseTo(p2, 2); // the flip price, not p1 — the key flip assertion
    expect(short!.exit).toBeCloseTo(p3, 2);
    expect(short!.realized).toBeCloseTo(expectedShortRealized, 2);

    const totalRealized = baselineRealized + expectedLongRealized + expectedShortRealized;
    await expect(page.getByTestId("trades-day-realized")).toHaveText(money(totalRealized));
  });
});
