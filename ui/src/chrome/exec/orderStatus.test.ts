import { describe, it, expect } from "vitest";
import { displayStatus, isWorking, isTerminal, STATUS_LABEL, sideIsSell, bareSymbol, abbrevType } from "./orderStatus";
import type { Order } from "../../wire/contract";

const base: Order = {
  venue: "alpaca-paper", id: "ET1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
  qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1000, updatedMs: 1000,
};

describe("orderStatus", () => {
  it("optimistic order shows PendingNew regardless of wire status", () => {
    expect(displayStatus(base, true)).toBe("PendingNew");
  });
  it("derives Replacing from a working order with a replacesId", () => {
    expect(displayStatus({ ...base, replacesId: "ET0", status: "ACCEPTED" }, false)).toBe("Replacing");
    expect(displayStatus({ ...base, replacesId: "ET0", status: "FILLED" }, false)).toBe("FILLED"); // terminal wins
  });
  it("passes through domain status when not optimistic and no replace", () => {
    expect(displayStatus({ ...base, status: "PARTIALLY_FILLED" }, false)).toBe("PARTIALLY_FILLED");
  });
  it("classifies working vs terminal", () => {
    expect(isWorking("SUBMITTED")).toBe(true);
    expect(isWorking("FILLED")).toBe(false);
    expect(isTerminal("CANCELED")).toBe(true);
    expect(isTerminal("ACCEPTED")).toBe(false);
  });
  it("labels every display status", () => {
    for (const k of ["PendingNew","Replacing","SUBMITTED","ACCEPTED","PARTIALLY_FILLED","FILLED","CANCELED","REJECTED","EXPIRED","BLOCKED","REPLACED"] as const)
      expect(STATUS_LABEL[k]).toBeTruthy();
  });
  it("side sell-ness, bare symbol, type abbreviation", () => {
    expect(sideIsSell("SELL")).toBe(true);
    expect(sideIsSell("SHORT")).toBe(true);
    expect(sideIsSell("BUY")).toBe(false);
    expect(bareSymbol("US.AAPL")).toBe("AAPL");
    expect(abbrevType("STOP_LIMIT")).toBe("STPLMT");
  });
});
