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
});
