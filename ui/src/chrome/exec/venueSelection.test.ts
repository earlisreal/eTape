import { describe, it, expect } from "vitest";
import { resolveVenue } from "./venueSelection";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { ExecStatus } from "../../wire/contract";

const status = (...ids: string[]): ExecStatus => ({
  masterArmed: false,
  global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: ids.map((venue) => ({
    venue, broker: "alpaca" as never, connected: true, reconcilePending: false, note: "", lastReconcileMs: null,
    gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
  })),
});

describe("resolveVenue", () => {
  it("prefers the group's focused venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(resolveVenue("green", lg, "alpaca-paper", status("alpaca-paper", "tradezero"))).toBe("tradezero");
  });

  it("falls back to the active venue, then the first configured venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue("green", lg, "alpaca-live", status("alpaca-paper", "alpaca-live"))).toBe("alpaca-live");
    // empty active venue (the default) falls through to the first venue
    expect(resolveVenue(null, lg, "", status("alpaca-paper", "alpaca-live"))).toBe("alpaca-paper");
  });

  it("returns empty string when nothing resolves", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "", null)).toBe("");
  });

  it("falls through when the active venue no longer exists in the current venue set", () => {
    // Reproduces the demo-mode transition bug: activeVenue still names the
    // pre-transition real venue (e.g. "alpaca-live"), but the engine
    // restarted into demo mode and now only runs "sim-paper" — the stale
    // activeVenue must not win the || chain over a venue that actually exists.
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "alpaca-live", status("sim-paper"))).toBe("sim-paper");
  });

  it("falls through when the group's focused venue no longer exists either", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(resolveVenue("green", lg, "alpaca-live", status("sim-paper"))).toBe("sim-paper");
  });

  it("does not resolve to an empty active venue when status is not loaded yet", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(resolveVenue(null, lg, "alpaca-live", null)).toBe("alpaca-live");
  });
});
