import { describe, it, expect, vi } from "vitest";
import { LinkGroups } from "./linkGroups";
import { FakeBus, FakeBusHub } from "../../test/fakes";

describe("LinkGroups", () => {
  it("focus updates local state, publishes on the bus, and echoes to the engine", () => {
    const hub = new FakeBusHub();
    const onEcho = vi.fn();
    const lg = new LinkGroups(new FakeBus(hub), onEcho);
    lg.focus("green", "US.AAPL");
    expect(lg.symbolFor("green")).toBe("US.AAPL");
    expect(onEcho).toHaveBeenCalledWith("green", "US.AAPL");
  });

  it("propagates focus across windows without an echo storm", () => {
    const hub = new FakeBusHub();
    const echoA = vi.fn();
    const echoB = vi.fn();
    const a = new LinkGroups(new FakeBus(hub), echoA);
    const b = new LinkGroups(new FakeBus(hub), echoB);
    a.focus("red", "US.TSLA");
    expect(b.symbolFor("red")).toBe("US.TSLA"); // B received it
    expect(echoB).not.toHaveBeenCalled();       // B does not re-echo remote focus
  });

  it("notifies subscribers on any focus change", () => {
    const hub = new FakeBusHub();
    const lg = new LinkGroups(new FakeBus(hub), () => {});
    const cb = vi.fn();
    lg.subscribe(cb);
    lg.focus("blue", "US.NVDA");
    expect(cb).toHaveBeenCalledTimes(1);
  });

  describe("focusChecked", () => {
    it("moves the group and broadcasts on an accepting ack", async () => {
      const hub = new FakeBusHub();
      const onEcho = vi.fn().mockResolvedValue({ kind: "ack" as const, corrId: "c", status: "accepted" as const });
      const lg = new LinkGroups(new FakeBus(hub), onEcho);
      const cb = vi.fn();
      lg.subscribe(cb);
      const r = await lg.focusChecked("blue", "US.NVDA");
      expect(r).toEqual({ ok: true });
      expect(lg.symbolFor("blue")).toBe("US.NVDA");
      expect(cb).toHaveBeenCalledTimes(1);
    });

    it("leaves the group unchanged and returns ok:false on a rejecting ack — never a half-switched group", async () => {
      const hub = new FakeBusHub();
      const onEcho = vi.fn().mockResolvedValue({ kind: "ack" as const, corrId: "c", status: "blocked" as const, reason: "unknown symbol" });
      const lg = new LinkGroups(new FakeBus(hub), onEcho);
      lg.focus("blue", "US.AAPL"); // pre-existing focus
      const cb = vi.fn();
      lg.subscribe(cb);
      const r = await lg.focusChecked("blue", "US.BOGUS");
      expect(r).toEqual({ ok: false, reason: "unknown symbol" });
      expect(lg.symbolFor("blue")).toBe("US.AAPL"); // unchanged
      expect(cb).not.toHaveBeenCalled(); // no spurious broadcast on reject
    });

    it("falls back to a generic reason when the ack omits one", async () => {
      const hub = new FakeBusHub();
      const onEcho = vi.fn().mockResolvedValue({ kind: "ack" as const, corrId: "c", status: "blocked" as const });
      const lg = new LinkGroups(new FakeBus(hub), onEcho);
      const r = await lg.focusChecked("red", "US.BOGUS");
      expect(r).toEqual({ ok: false, reason: "symbol rejected" });
    });

    it("treats a void-returning onEcho (no ack promise) as accepted, for callers that don't validate", async () => {
      const hub = new FakeBusHub();
      const onEcho = vi.fn(); // returns undefined, like a fire-and-forget echo
      const lg = new LinkGroups(new FakeBus(hub), onEcho);
      const r = await lg.focusChecked("yellow", "US.MSFT");
      expect(r).toEqual({ ok: true });
      expect(lg.symbolFor("yellow")).toBe("US.MSFT");
    });
  });

  describe("hydrate/snapshot (Bug 5: restore the per-group focus a page refresh would otherwise lose)", () => {
    it("hydrate seeds the focused map; snapshot returns it as a plain object", () => {
      const hub = new FakeBusHub();
      const lg = new LinkGroups(new FakeBus(hub), () => {});
      lg.hydrate({ green: "US.TSLA", blue: "US.NVDA" });
      expect(lg.symbolFor("green")).toBe("US.TSLA");
      expect(lg.symbolFor("blue")).toBe("US.NVDA");
      expect(lg.symbolFor("red")).toBeUndefined();
      expect(lg.snapshot()).toEqual({ green: "US.TSLA", blue: "US.NVDA" });
    });

    it("hydrate does not broadcast to other windows, echo to the engine, or notify subscribers", () => {
      const hub = new FakeBusHub();
      const onEcho = vi.fn();
      const lg = new LinkGroups(new FakeBus(hub), onEcho);
      const other = new LinkGroups(new FakeBus(hub), () => {}); // a second "window"
      const cb = vi.fn();
      lg.subscribe(cb);
      lg.hydrate({ green: "US.TSLA" });
      expect(onEcho).not.toHaveBeenCalled();
      expect(cb).not.toHaveBeenCalled();
      expect(other.symbolFor("green")).toBeUndefined(); // no cross-window post either
    });

    it("hydrate skips falsy symbol entries", () => {
      const hub = new FakeBusHub();
      const lg = new LinkGroups(new FakeBus(hub), () => {});
      lg.hydrate({ green: "" });
      expect(lg.symbolFor("green")).toBeUndefined();
    });
  });

  describe("venue focus", () => {
    it("focusVenue updates local state and posts to the bus without an engine echo", () => {
      const onEcho = vi.fn();
      const lg = new LinkGroups(new FakeBus(new FakeBusHub()), onEcho);
      lg.focusVenue("green", "alpaca-paper");
      expect(lg.venueFor("green")).toBe("alpaca-paper");
      expect(onEcho).not.toHaveBeenCalled(); // venue is UI-only state — never validated server-side
    });

    it("venueFor returns undefined for the pinned (null) group", () => {
      const lg = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
      expect(lg.venueFor(null)).toBeUndefined();
    });

    it("propagates a venue-only message across windows without touching symbol", () => {
      const hub = new FakeBusHub();
      const a = new LinkGroups(new FakeBus(hub), () => {});
      const b = new LinkGroups(new FakeBus(hub), () => {});
      a.focus("green", "US.AAPL");
      b.focusVenue("green", "tradezero");
      expect(a.venueFor("green")).toBe("tradezero"); // venue crossed the bus
      expect(a.symbolFor("green")).toBe("US.AAPL");   // symbol untouched by the venue message
    });

    it("hydrateVenues/snapshotVenues round-trip and skip falsy entries", () => {
      const lg = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
      lg.hydrateVenues({ green: "alpaca-paper", red: "" as never });
      expect(lg.snapshotVenues()).toEqual({ green: "alpaca-paper" });
    });
  });
});
