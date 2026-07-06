import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";
import type { AccountRow } from "../wire/contract";

// Superseded by the dedicated coverage in ExecStore.test.ts (Plan 5) — kept as a
// minimal smoke test against the public accounts() accessor, since ExecState is
// now a keyed Map internal to the store (no direct .account field to assert on).
describe("ExecStore", () => {
  it("upserts an account row on snapshot", () => {
    const s = new ExecStore();
    const row: AccountRow = {
      venue: "alpaca-paper", equity: 1000, buyingPower: 4000, availableCash: 500,
      sodEquity: 1000, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1,
    };
    s.apply({ kind: "snapshot", topic: "exec.account", payload: row });
    expect(s.accounts()).toContainEqual(row);
  });
});
