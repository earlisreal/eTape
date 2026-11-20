import { describe, it, expect } from "vitest";
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

describe("Link Group venue resolution", () => {
  it("prefers the group's focused venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(lg.venueFor("green", status("alpaca-paper", "tradezero"))).toBe("tradezero");
  });

  it("does not fall back to a global or first configured venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    expect(lg.venueFor("green", status("alpaca-paper", "alpaca-live"))).toBeUndefined();
    expect(lg.venueFor(null, status("alpaca-paper", "alpaca-live"))).toBeUndefined();
  });

  it("returns empty when the group's focused venue no longer exists", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "tradezero");
    expect(lg.venueFor("green", status("sim-paper"))).toBeUndefined();
  });

  it("restores a legacy perpetual venue only for its exchange and keeps it when adding a stock venue", () => {
    const stocks = status("alpaca-paper", "mexc-paper");
    const st: ExecStatus = { ...stocks, venues: [stocks.venues[0], { ...stocks.venues[1], broker: "mexc-sim" }] };
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {}, () => st);
    lg.hydrateVenues({ green: "mexc-paper" });

    lg.focus("green", "US.NVDA");
    expect(lg.venueFor("green")).toBeUndefined();
    lg.focusVenue("green", "alpaca-paper");
    lg.focus("green", "PERP.MEXC.BTC_USDT");
    expect(lg.venueFor("green")).toBe("mexc-paper");
    lg.focus("green", "PERP.BINGX.BTC_USDT");
    expect(lg.venueFor("green")).toBeUndefined();
    lg.focus("green", "US.NVDA");
    expect(lg.venueFor("green")).toBe("alpaca-paper");
  });

  it("restores a remembered live stock venue after paper crypto, including on reload", () => {
    const st = status("alpaca-live", "mexc-paper");
    st.venues[0] = { ...st.venues[0], env: "live" };
    st.venues[1] = { ...st.venues[1], broker: "mexc-sim", env: "paper" };
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {}, () => st);
    lg.hydrateVenues({ green: { stocks: "alpaca-live", MEXC: "mexc-paper" } });
    lg.focus("green", "PERP.MEXC.BTC_USDT");
    lg.focus("green", "PERP.BINGX.BTC_USDT");
    lg.focus("green", "US.NVDA");
    expect(lg.venueFor("green")).toBe("alpaca-live");
    expect(lg.awaitingLiveConfirmation("green", st)).toBe(false);
    expect(lg.needsLiveConfirmation("green", "alpaca-live")).toBe(false);
    expect(lg.matchesOrderContext("green", "US.NVDA", "alpaca-live")).toBe(true);

    const restored = new LinkGroups(new BroadcastChannelBus(), () => {}, () => st);
    restored.hydrate(lg.snapshot());
    restored.hydrateVenues(lg.snapshotVenues());
    expect(restored.venueFor("green")).toBe("alpaca-live");
  });

  it("still asks for confirmation before restoring a live perpetual venue after paper", () => {
    const st = status("alpaca-paper", "mexc-live");
    st.venues[0] = { ...st.venues[0], env: "paper" };
    st.venues[1] = { ...st.venues[1], broker: "mexc", env: "live" };
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {}, () => st);
    lg.hydrateVenues({ green: { stocks: "alpaca-paper", MEXC: "mexc-live" } });

    lg.focusMarket("green", "BINGX");
    lg.focusMarket("green", "MEXC");
    expect(lg.symbolFor("green")).toBe("");
    expect(lg.venueFor("green")).toBeUndefined();
    expect(lg.awaitingLiveConfirmation("green", st)).toBe(true);
    expect(lg.needsLiveConfirmation("green", "mexc-live")).toBe(true);

    lg.focusVenue("green", "mexc-live");
    expect(lg.venueFor("green")).toBe("mexc-live");
  });

  it("waits for status before using a persisted venue", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    lg.focusVenue("green", "alpaca-live");
    expect(lg.venueFor("green", null)).toBeUndefined();
  });
});
