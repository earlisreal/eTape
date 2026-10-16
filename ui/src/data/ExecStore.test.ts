import { describe, it, expect, vi } from "vitest";
import { ExecStore } from "./ExecStore";
import type { Order, AccountRow, ExecStatus } from "../wire/contract";

const snap = (topic: string, payload: unknown, key?: string) => ({ kind: "snapshot" as const, topic: topic as never, ...(key !== undefined ? { key } : {}), payload });
const delta = (topic: string, payload: unknown, key?: string) => ({ kind: "delta" as const, topic: topic as never, ...(key !== undefined ? { key } : {}), payload });

const order = (id: string, over: Partial<Order> = {}): Order => ({
  venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
  qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over,
});
const acct = (venue: string, dayPnl: number): AccountRow => ({
  venue, equity: 100, buyingPower: 400, availableCash: 50, sodEquity: 100, realized: 0, dayPnl, leverage: 4, tsMs: 1, cycleStartMs: 0, cycleRealized: 0,
});
const status: ExecStatus = {
  masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, positionDataReady: true, flattenPending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
};
const riskEntry: NonNullable<Order["riskEntry"]> = { stopId: "stop", budget: 100, mode: "Dollar", buyCushion: { value: 0, unit: "$" }, sellCushion: { value: 0, unit: "$" } };
const activationUnknown = (id: string, over: Partial<Order> = {}) => order(id, {
  riskEntry, held: { phase: "UNKNOWN", deadlineMs: 100, pausedReason: "activation outcome unknown: timeout" }, ...over,
});

describe("ExecStore.onRiskActivationUnknown", () => {
  it("reports new uncertainty once, suppresses snapshot history, and permits a later rejection", () => {
    const store = new ExecStore(), unknown = vi.fn(), rejected = vi.fn();
    store.onRiskActivationUnknown(unknown); store.onOrderRejected(rejected);
    store.apply(snap("exec.orders", [activationUnknown("old")]));
    store.apply(delta("exec.orders", activationUnknown("old")));
    expect(unknown).not.toHaveBeenCalled();
    store.apply(delta("exec.orders", activationUnknown("new")));
    store.apply(delta("exec.orders", activationUnknown("new")));
    store.apply(snap("exec.orders", [activationUnknown("new")]));
    store.apply(delta("exec.orders", activationUnknown("new")));
    expect(unknown).toHaveBeenCalledTimes(1);
    store.apply(delta("exec.orders", activationUnknown("new", { status: "REJECTED", rejectReason: "broker rejected" })));
    expect(rejected).toHaveBeenCalledTimes(1);
    expect(unknown).toHaveBeenCalledTimes(1);
  });
  it("excludes protection, unrelated uncertainty and terminal rows retaining held metadata", () => {
    const store = new ExecStore(), cb = vi.fn();
    store.onRiskActivationUnknown(cb);
    const protection = activationUnknown("protection", { riskEntryId: "entry" });
    delete protection.riskEntry;
    store.apply(delta("exec.orders", protection));
    const exclusions: Partial<Order>[] = [
      { held: { phase: "UNKNOWN", deadlineMs: 100, pausedReason: "restart reconciliation required" } },
      { held: { phase: "UNKNOWN", deadlineMs: 100, pausedReason: "cancel outcome unknown" } },
      { held: { phase: "ACTIVATING", deadlineMs: 100 } },
      ...(["REJECTED", "CANCELED", "EXPIRED", "FILLED"] as const).map(status => ({ status })),
    ];
    exclusions.forEach((over, i) => store.apply(delta("exec.orders", activationUnknown(String(i), over))));
    expect(cb).not.toHaveBeenCalled();
  });
  it("isolates listener errors and removes subscriptions", () => {
    const store = new ExecStore(), cb = vi.fn();
    const offBad = store.onRiskActivationUnknown(() => { throw new Error("listener failed"); });
    const off = store.onRiskActivationUnknown(cb);
    store.apply(delta("exec.orders", activationUnknown("first")));
    expect(cb).toHaveBeenCalledTimes(1);
    expect(store.orders()).toHaveLength(1);
    offBad(); off();
    store.apply(delta("exec.orders", activationUnknown("second")));
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("ExecStore", () => {
  it("keyed account upsert by venue", () => {
    const s = new ExecStore();
    s.apply(snap("exec.account", acct("alpaca-paper", 5), "alpaca-paper"));
    s.apply(delta("exec.account", acct("tradezero-live", -3), "tradezero-live"));
    s.apply(delta("exec.account", acct("alpaca-paper", 9), "alpaca-paper")); // upsert, not append
    expect(s.accounts()).toHaveLength(2);
    expect(s.accounts().find((a) => a.venue === "alpaca-paper")?.dayPnl).toBe(9);
  });
  it("keyed order upsert by id (snapshot replaces, delta upserts)", () => {
    const s = new ExecStore();
    s.apply(snap("exec.orders", [order("ET1"), order("ET2")]));
    s.apply(delta("exec.orders", order("ET1", { status: "FILLED", executedQty: 10, leavesQty: 0 }), "ET1"));
    const views = s.orders();
    expect(views).toHaveLength(2);
    expect(views.find((v) => v.order.id === "ET1")?.order.status).toBe("FILLED");
  });
  it("positions + status full-replace", () => {
    const s = new ExecStore();
    s.apply(snap("exec.positions", [{ venue: "alpaca-paper", symbol: "US.AAPL", qty: 5, avgPrice: 3, unrealizedPnl: 1 }]));
    s.apply(delta("exec.positions", [])); // full replace to empty
    expect(s.positions()).toHaveLength(0);
    s.apply(snap("exec.status", status));
    expect(s.status()?.masterArmed).toBe(true);
  });
  it("optimistic row appears then reconciles when the real order event lands", () => {
    const s = new ExecStore();
    s.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 });
    expect(s.orders()).toHaveLength(1);
    expect(s.orders()[0].optimistic).toBe(true);
    s.apply(delta("exec.orders", order("ET9", { status: "SUBMITTED" }), "ET9"));
    expect(s.orders()).toHaveLength(1);          // reconciled, not doubled
    expect(s.orders()[0].optimistic).toBe(false);
  });
  it("workingOrdersFor filters by symbol and working status", () => {
    const s = new ExecStore();
    s.apply(snap("exec.orders", [order("ET1", { status: "ACCEPTED" }), order("ET2", { status: "FILLED" }), order("ET3", { symbol: "US.NVDA", status: "SUBMITTED" })]));
    expect(s.workingOrdersFor("US.AAPL").map((o) => o.id)).toEqual(["ET1"]);
    expect(s.workingOrdersFor().map((o) => o.id).sort()).toEqual(["ET1", "ET3"]);
  });
  it("does not offer cancel-requested intents to Cancel Last or Cancel All", () => {
    const s = new ExecStore();
    s.apply(snap("exec.orders", [
      order("ET1"),
      order("ET2", { held:{phase:"CANCEL_REQUESTED", deadlineMs:3, cancelRequested:true} }),
      order("ET3", { action:{kind:"CANCEL", phase:"UNKNOWN"} }),
    ]));
    expect(s.workingOrdersFor().map((o) => o.id)).toEqual(["ET1"]);
  });
});

describe("ExecStore.onOrderRejected", () => {
  it("fires on a delta transition into REJECTED", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "SUBMITTED" }) });
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED", rejectReason: "no shares" }) });
    expect(cb).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledWith(expect.objectContaining({ id: "o1", status: "REJECTED" }));
  });

  it("does not fire when a REJECTED row seeds via snapshot", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "snapshot", topic: "exec.orders", payload: [order("o1", { status: "REJECTED" })] });
    expect(cb).not.toHaveBeenCalled();
  });

  it("does not re-fire when an already-REJECTED row is re-sent unchanged (delta)", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED" }) }); // no prior row -> fires once
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED" }) }); // unchanged -> silent
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
