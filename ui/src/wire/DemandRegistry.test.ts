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

  it("re-announces every live demand on reconnect (state=open)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p2", "US.MSFT", "focused");
    f.sent.length = 0;
    f.fireState("open");
    expect(f.sent).toEqual([
      { name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } },
      { name: "EnsureSymbol", args: { demandId: "p2", symbol: "US.MSFT", profile: "focused" } },
    ]);
  });
});
