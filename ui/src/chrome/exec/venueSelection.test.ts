import { describe, it, expect } from "vitest";
import { resolveVenue } from "./venueSelection";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { ExecStatus } from "../../wire/contract";

const status = (...ids: string[]): ExecStatus => ({
  masterArmed: false,
  global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: ids.map((venue) => ({
    venue, broker: "alpaca" as never, connected: true, reconcilePending: false, positionDataReady: true, flattenPending: false, note: "", lastReconcileMs: null,
    gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
  })),
});

describe("resolveVenue", () => {
  it("prefers the group's focused venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(resolveVenue("green", lg, "alpaca-paper", status("alpaca-paper", "tradezero"))).toBe("tradezero");
  });

  it("does not fall back to a global or first configured venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue("green", lg, "alpaca-live", status("alpaca-paper", "alpaca-live"))).toBe("");
    expect(resolveVenue(null, lg, "", status("alpaca-paper", "alpaca-live"))).toBe("");
  });

  it("returns empty string when nothing resolves", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "", null)).toBe("");
  });

  it("does not use a stale global venue after a mode transition", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "alpaca-live", status("sim-paper"))).toBe("");
  });

  it("returns empty when the group's focused venue no longer exists", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(resolveVenue("green", lg, "alpaca-live", status("sim-paper"))).toBe("");
  });

  it("still allows a persisted group venue before status is loaded", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "alpaca-live");
    expect(resolveVenue("green", lg, undefined, null)).toBe("alpaca-live");
  });
});
