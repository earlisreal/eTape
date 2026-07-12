import { describe, it, expect } from "vitest";
import { DemandRegistry } from "./DemandRegistry";
import type { AckMsg } from "./contract";
import type { ConnState } from "./WsClient";

function fakeClient() {
  const sent: { name: string; args: any }[] = [];
  let stateCb: ((s: ConnState) => void) | null = null;
  let nextAck: AckMsg = { kind: "ack", corrId: "", status: "accepted" };
  return {
    sent,
    setAck: (a: AckMsg) => { nextAck = a; },
    fireState: (s: ConnState) => stateCb?.(s),
    client: {
      sendCommand: (name: string, args: unknown) => { sent.push({ name, args }); return Promise.resolve(nextAck); },
      onState: (cb: (s: ConnState) => void) => { stateCb = cb; },
    },
  };
}

// A client whose sendCommand promise resolution is controlled manually, so
// tests can interleave a release() in between an ensure() call and the
// moment its EnsureSymbol ack actually resolves.
function controlledClient() {
  const sent: { name: string; args: any }[] = [];
  let stateCb: ((s: ConnState) => void) | null = null;
  const resolvers: Array<(ack: AckMsg) => void> = [];
  return {
    sent,
    resolvers,
    fireState: (s: ConnState) => stateCb?.(s),
    client: {
      sendCommand: (name: string, args: unknown) => {
        sent.push({ name, args });
        return new Promise<AckMsg>((resolve) => resolvers.push(resolve));
      },
      onState: (cb: (s: ConnState) => void) => { stateCb = cb; },
    },
  };
}

describe("DemandRegistry", () => {
  it("ensure sends EnsureSymbol and records on accept", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    const ack = await reg.ensure("p1", "US.AAPL", "watch");
    expect(ack.status).toBe("accepted");
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);
  });

  it("dedupes an unchanged symbol+profile without sending", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    const ack = await reg.ensure("p1", "US.AAPL", "watch");
    expect(ack.status).toBe("accepted");
    expect(f.sent.length).toBe(1); // second call is a no-op
  });

  it("re-sends on a symbol switch", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p1", "US.MSFT", "watch");
    expect(f.sent.length).toBe(2);
  });

  it("does not record on a blocked ack (so a retry re-sends)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    f.setAck({ kind: "ack", corrId: "", status: "blocked", reason: "unknown symbol US.X" });
    const ack = await reg.ensure("p1", "US.X", "watch");
    expect(ack.status).toBe("blocked");
    f.setAck({ kind: "ack", corrId: "", status: "accepted" });
    await reg.ensure("p1", "US.X", "watch");
    expect(f.sent.length).toBe(2); // not deduped — first was never recorded
  });

  it("release sends ReleaseSymbol and forgets", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    reg.release("p1");
    expect(f.sent.at(-1)).toEqual({ name: "ReleaseSymbol", args: { demandId: "p1" } });
    // releasing an unknown panel is a no-op
    reg.release("nope");
    expect(f.sent.length).toBe(2);
  });

  it("release() before an in-flight ensure() resolves sends ReleaseSymbol exactly once and leaves no phantom live entry", async () => {
    const f = controlledClient();
    const reg = new DemandRegistry(f.client);

    // Panel mounts, firing ensure(); the round-trip hasn't resolved yet.
    const ensurePromise = reg.ensure("p1", "US.AAPL", "watch");
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);

    // Panel unmounts before the ack arrives.
    reg.release("p1");

    // Now the in-flight EnsureSymbol ack resolves, accepted.
    f.resolvers[0]({ kind: "ack", corrId: "", status: "accepted" });
    await ensurePromise;

    // Exactly one ReleaseSymbol was sent — not zero (the old bug: release()
    // no-oped because `live` didn't have the panel yet) and not duplicated.
    const releaseCalls = f.sent.filter((c) => c.name === "ReleaseSymbol");
    expect(releaseCalls).toEqual([{ name: "ReleaseSymbol", args: { demandId: "p1" } }]);

    // No phantom `live` entry: a subsequent ensure() for the identical
    // symbol+profile must NOT dedupe (it must re-send), because the panel
    // is no longer considered live.
    f.sent.length = 0;
    const ack2 = reg.ensure("p1", "US.AAPL", "watch");
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);
    // resolvers[0] = the original in-flight EnsureSymbol (already resolved above),
    // resolvers[1] = release()'s fire-and-forget ReleaseSymbol (left unresolved,
    // nothing awaits it), resolvers[2] = this second ensure()'s EnsureSymbol.
    f.resolvers[2]({ kind: "ack", corrId: "", status: "accepted" });
    await ack2;

    // And reannounce (WS reconnect) must not re-assert the phantom demand —
    // by now p1 is genuinely live again from the re-ensure above, so this
    // just confirms reannounce reflects the real (single) live entry, not a
    // leaked duplicate.
    f.sent.length = 0;
    f.fireState("open");
    // reannounce() is async (it awaits the injected reannounceGate, which
    // defaults to an already-resolved promise) — one microtask tick for the
    // send loop to run after the synchronous fireState() call returns.
    await Promise.resolve();
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);
  });

  it("re-announces every live demand on reconnect (state=open)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p2", "US.MSFT", "focused");
    f.sent.length = 0;
    f.fireState("open");
    await Promise.resolve(); // let the default (immediately-resolving) gate settle
    expect(f.sent).toEqual([
      { name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } },
      { name: "EnsureSymbol", args: { demandId: "p2", symbol: "US.MSFT", profile: "focused" } },
    ]);
  });

  it("omitting the reannounceGate arg preserves today's immediate reannounce (no regression for callers that don't pass one)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client); // 1-arg construction, same as production callers
    await reg.ensure("p1", "US.AAPL", "watch");
    f.sent.length = 0;
    f.fireState("open");
    await Promise.resolve();
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);
  });

  it("a never-resolving injected gate defers all EnsureSymbol sends", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client, () => new Promise<void>(() => {})); // never resolves
    await reg.ensure("p1", "US.AAPL", "watch");
    f.sent.length = 0;
    f.fireState("open");
    await Promise.resolve();
    await Promise.resolve();
    expect(f.sent).toEqual([]); // gate never resolved — reannounce is stuck awaiting it
  });

  it("re-announces every live demand once the injected gate resolves", async () => {
    const f = fakeClient();
    let releaseGate: () => void = () => {};
    const gate = () => new Promise<void>((resolve) => { releaseGate = resolve; });
    const reg = new DemandRegistry(f.client, gate);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p2", "US.MSFT", "focused");
    f.sent.length = 0;
    f.fireState("open");
    await Promise.resolve();
    expect(f.sent).toEqual([]); // still gated — nothing sent yet

    releaseGate();
    await Promise.resolve();
    expect(f.sent).toEqual([
      { name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } },
      { name: "EnsureSymbol", args: { demandId: "p2", symbol: "US.MSFT", profile: "focused" } },
    ]);
  });
});
