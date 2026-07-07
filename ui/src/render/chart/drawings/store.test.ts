import { describe, it, expect, vi } from "vitest";
import { DrawingStore } from "./store";
import type { Drawing } from "./model";
import { FakeDrawingBus, FakeDrawingBusHub } from "../../../../test/fakes";

const mk = (id: string, symbol: string): Drawing => ({
  id, symbol, kind: "hline", anchors: [{ timeMs: 1000, price: 10 }], createdMs: 1, updatedMs: 1,
});

describe("DrawingStore core", () => {
  it("upsert adds a drawing under its symbol and bumps the revision", () => {
    const s = new DrawingStore();
    const r0 = s.getRev();
    s.upsert(mk("a", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("upsert replaces an existing id in place (no duplicate)", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert({ ...mk("a", "US.AAPL"), anchors: [{ timeMs: 1000, price: 99 }] });
    const arr = s.forSymbol("US.AAPL");
    expect(arr).toHaveLength(1);
    expect(arr[0].anchors[0].price).toBe(99);
  });

  it("forSymbol returns [] for an unknown symbol and isolates symbols", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.TSLA"));
    expect(s.forSymbol("US.NVDA")).toEqual([]);
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    expect(s.forSymbol("US.TSLA").map((d) => d.id)).toEqual(["b"]);
  });

  it("remove deletes by id (looking up its symbol) and bumps the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.AAPL"));
    const r0 = s.getRev();
    s.remove("a");
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["b"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("remove of an unknown id is a no-op and does not bump the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    const r0 = s.getRev();
    s.remove("zzz");
    expect(s.getRev()).toBe(r0);
    expect(s.forSymbol("US.AAPL")).toHaveLength(1);
  });

  it("clearSymbol empties one symbol only and bumps the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.TSLA"));
    const r0 = s.getRev();
    s.clearSymbol("US.AAPL");
    expect(s.forSymbol("US.AAPL")).toEqual([]);
    expect(s.forSymbol("US.TSLA").map((d) => d.id)).toEqual(["b"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("preserves insertion order within a symbol", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.AAPL"));
    s.upsert(mk("c", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a", "b", "c"]);
  });
});

interface FakeAck { status: string; value?: unknown; reason?: string }
function fakeCommands(overrides?: Partial<{ get: FakeAck; set: FakeAck }>) {
  const calls: { name: string; args: any }[] = [];
  const sendCommand = vi.fn(async (name: string, args: unknown): Promise<FakeAck> => {
    calls.push({ name, args });
    if (name === "GetConfig") return overrides?.get ?? { status: "accepted", value: [] };
    return overrides?.set ?? { status: "accepted" };
  });
  return { sendCommand, calls };
}

describe("DrawingStore sync + persistence", () => {
  const mk = (id: string, symbol: string): Drawing => ({
    id, symbol, kind: "hline", anchors: [{ timeMs: 1000, price: 10 }], createdMs: 1, updatedMs: 1,
  });

  it("local upsert publishes on the bus and schedules a persist", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands();
    const s = new DrawingStore(0); // debounceMs 0
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    const set = cmd.calls.find((c) => c.name === "SetConfig");
    expect(set?.args.key).toBe("drawings.US.AAPL");
    expect((set?.args.value as Drawing[]).map((d) => d.id)).toEqual(["a"]);
  });

  it("propagates a local upsert to another window without an echo storm", async () => {
    const hub = new FakeDrawingBusHub();
    const cmdA = fakeCommands();
    const cmdB = fakeCommands();
    const a = new DrawingStore(0);
    const b = new DrawingStore(0);
    a.connect({ commands: cmdA, bus: new FakeDrawingBus(hub), onError: () => {} });
    b.connect({ commands: cmdB, bus: new FakeDrawingBus(hub), onError: () => {} });
    a.upsert(mk("a", "US.AAPL"));
    // B received the drawing…
    expect(b.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    // …but B must NOT re-persist a remotely-applied drawing (single-writer).
    await b.flush();
    expect(cmdB.calls.some((c) => c.name === "SetConfig")).toBe(false);
  });

  it("remote remove/clear apply locally without re-publishing", async () => {
    const hub = new FakeDrawingBusHub();
    const a = new DrawingStore(0);
    const b = new DrawingStore(0);
    a.connect({ commands: fakeCommands(), bus: new FakeDrawingBus(hub), onError: () => {} });
    b.connect({ commands: fakeCommands(), bus: new FakeDrawingBus(hub), onError: () => {} });
    a.upsert(mk("a", "US.AAPL"));
    a.remove("a");
    expect(b.forSymbol("US.AAPL")).toEqual([]);
    a.upsert(mk("c", "US.AAPL"));
    a.clearSymbol("US.AAPL");
    expect(b.forSymbol("US.AAPL")).toEqual([]);
  });

  it("ensureLoaded fetches once per symbol per session and validates", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands({ get: { status: "accepted", value: [mk("x", "US.AAPL"), { junk: true }] } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.ensureLoaded("US.AAPL");
    s.ensureLoaded("US.AAPL"); // second call must not refetch
    // vi.fn-wrapped async mocks resolve a couple microtask ticks later than a
    // plain async function (measured empirically against this Vitest/tinyspy
    // version), so drain more ticks than a bare `await Promise.resolve()` pair
    // to let the GetConfig .then() handler (and its setLocal calls) run.
    for (let i = 0; i < 5; i++) await Promise.resolve();
    expect(cmd.calls.filter((c) => c.name === "GetConfig")).toHaveLength(1);
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["x"]); // junk dropped
  });

  it("ensureLoaded is a no-op (empty list) when the store is not connected", async () => {
    const s = new DrawingStore(0);
    s.ensureLoaded("US.AAPL");
    await Promise.resolve();
    expect(s.forSymbol("US.AAPL")).toEqual([]);
  });

  it("a loaded drawing is not re-persisted", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands({ get: { status: "accepted", value: [mk("x", "US.AAPL")] } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.ensureLoaded("US.AAPL");
    await Promise.resolve(); await Promise.resolve();
    await s.flush();
    expect(cmd.calls.some((c) => c.name === "SetConfig")).toBe(false);
  });

  it("clearSymbol persists an empty array (so a restart does not reload)", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands();
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    s.clearSymbol("US.AAPL");
    await s.flush();
    const last = [...cmd.calls].reverse().find((c) => c.name === "SetConfig");
    expect(last?.args.key).toBe("drawings.US.AAPL");
    expect(last?.args.value).toEqual([]);
  });

  it("surfaces a save failure via onError and keeps the symbol dirty for retry", async () => {
    const hub = new FakeDrawingBusHub();
    const onError = vi.fn();
    const cmd = fakeCommands({ set: { status: "blocked", reason: "disk full" } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    expect(onError).toHaveBeenCalledWith("disk full");
    // still dirty → a subsequent flush retries the write
    const before = cmd.calls.filter((c) => c.name === "SetConfig").length;
    await s.flush();
    expect(cmd.calls.filter((c) => c.name === "SetConfig").length).toBeGreaterThan(before);
  });

  it("disconnected upsert still works in-memory and never persists", async () => {
    const s = new DrawingStore(0);
    s.upsert(mk("a", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    await s.flush(); // no throw, no deps
  });
});
