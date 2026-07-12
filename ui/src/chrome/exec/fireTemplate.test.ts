import { describe, it, expect, vi } from "vitest";
import { fireTemplate, type FireContext } from "./fireTemplate";
import type { OrderCommands } from "./commands";
import type { ToastApi } from "../Toast";
import type { PlaceOrderTemplate, ManagementTemplate, ManagementAction } from "./actionTemplate";
import type { Quote } from "../../wire/contract";

const QUOTE: Quote = { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" };
// Wednesday 2026-01-07 10:00 ET (RTH: 09:30-16:00) — avoids the Market/RTH
// coercion notice in tests that don't care about it.
const RTH_NOW = Date.UTC(2026, 0, 7, 15, 0, 0);
// Wednesday 2026-01-07 02:00 ET — outside RTH, drives the Market coercion notice.
const NOT_RTH_NOW = Date.UTC(2026, 0, 7, 7, 0, 0);

function makeOc(): OrderCommands {
  return {
    submit: vi.fn(async () => {}),
    cancel: vi.fn(async () => {}),
    replace: vi.fn(async () => {}),
    flatten: vi.fn(async () => {}),
    arm: vi.fn(async () => {}),
    disarm: vi.fn(async () => {}),
    kill: vi.fn(async () => {}),
    cancelLast: vi.fn(async () => {}),
    cancelAll: vi.fn(async () => {}),
  } as unknown as OrderCommands;
}
function makeToast(): ToastApi {
  return { push: vi.fn(), dismiss: vi.fn() };
}
function baseCtx(overrides: Partial<FireContext> = {}): FireContext {
  return {
    venue: "alpaca-paper", symbol: "US.AAPL", quote: QUOTE,
    buyingPower: 100_000, positionQty: 0, armed: true, nowMs: RTH_NOW,
    extHoursMarketBufferPct: 1,
    ...overrides,
  };
}
const PLACE_TEMPLATE: PlaceOrderTemplate = {
  kind: "place", id: "buy-5k", label: "Buy $5k",
  side: "BUY", type: "LIMIT", tif: "DAY",
  priceSource: "Ask", priceOffset: 0,
  sizing: { mode: "Dollar", dollar: 5000 },
};

describe("fireTemplate — place templates", () => {
  it("blocks and toasts when gateArm:true and disarmed; does not submit", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(PLACE_TEMPLATE, baseCtx({ armed: false }), oc, toast, { gateArm: true });
    expect(toast.push).toHaveBeenCalledWith({ level: "warn", text: "locked — hotkey blocked" });
    expect(oc.submit).not.toHaveBeenCalled();
  });

  it("submits when gateArm:false even if disarmed (deck's always-submit behavior)", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(PLACE_TEMPLATE, baseCtx({ armed: false }), oc, toast, { gateArm: false });
    expect(oc.submit).toHaveBeenCalledTimes(1);
  });

  it("submits when gateArm:true and armed", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(PLACE_TEMPLATE, baseCtx({ armed: true }), oc, toast, { gateArm: true });
    expect(oc.submit).toHaveBeenCalledTimes(1);
  });

  it("danger-toasts and does not submit when quote is missing", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(PLACE_TEMPLATE, baseCtx({ quote: undefined }), oc, toast, { gateArm: false });
    expect(toast.push).toHaveBeenCalledWith({ level: "danger", text: "no venue/quote for order" });
    expect(oc.submit).not.toHaveBeenCalled();
  });

  it("danger-toasts and does not submit when venue is empty", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(PLACE_TEMPLATE, baseCtx({ venue: "" }), oc, toast, { gateArm: false });
    expect(toast.push).toHaveBeenCalledWith({ level: "danger", text: "no venue/quote for order" });
    expect(oc.submit).not.toHaveBeenCalled();
  });

  it("surfaces preCheck notices as warn toasts and, on failure, joined errors as a danger toast; does not submit", () => {
    const oc = makeOc();
    const toast = makeToast();
    // MARKET outside RTH -> marketable-limit conversion notice; Shares:0 -> qty error (ok:false).
    const t: PlaceOrderTemplate = {
      kind: "place", id: "mkt-0", label: "Market 0",
      side: "BUY", type: "MARKET", tif: "DAY",
      priceSource: "Last", priceOffset: 0,
      sizing: { mode: "Shares", shares: 0 },
    };
    fireTemplate(t, baseCtx({ nowMs: NOT_RTH_NOW }), oc, toast, { gateArm: false });
    expect(toast.push).toHaveBeenCalledWith({ level: "warn", text: expect.stringContaining("Limit @") });
    expect(toast.push).toHaveBeenCalledWith({ level: "danger", text: "Quantity must be greater than 0." });
    expect(oc.submit).not.toHaveBeenCalled();
  });
});

describe("fireTemplate — management templates", () => {
  const manage = (action: ManagementAction): ManagementTemplate => ({ kind: "manage", id: action, label: action, action });

  it("CancelLast calls oc.cancelLast(symbol), even when disarmed", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(manage("CancelLast"), baseCtx({ armed: false, symbol: "US.AAPL" }), oc, toast, { gateArm: true });
    expect(oc.cancelLast).toHaveBeenCalledWith("US.AAPL");
  });

  it("CancelLast passes undefined for an empty symbol", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(manage("CancelLast"), baseCtx({ symbol: "" }), oc, toast, { gateArm: true });
    expect(oc.cancelLast).toHaveBeenCalledWith(undefined);
  });

  it("CancelAllFocused calls oc.cancelAll('focused', symbol), even when disarmed", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(manage("CancelAllFocused"), baseCtx({ armed: false, symbol: "US.AAPL" }), oc, toast, { gateArm: true });
    expect(oc.cancelAll).toHaveBeenCalledWith("focused", "US.AAPL");
  });

  it("CancelAllEverything calls oc.cancelAll('everything'), even when disarmed", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(manage("CancelAllEverything"), baseCtx({ armed: false }), oc, toast, { gateArm: true });
    expect(oc.cancelAll).toHaveBeenCalledWith("everything");
  });

  it("KillSwitch calls oc.kill() and warn-toasts, even when disarmed", () => {
    const oc = makeOc();
    const toast = makeToast();
    fireTemplate(manage("KillSwitch"), baseCtx({ armed: false }), oc, toast, { gateArm: true });
    expect(oc.kill).toHaveBeenCalledWith();
    expect(toast.push).toHaveBeenCalledWith({ level: "warn", text: "KILL — cancel-all + lock" });
  });
});
