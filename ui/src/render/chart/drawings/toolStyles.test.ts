import { describe, it, expect, vi } from "vitest";
import { DrawingToolStyleStore } from "./toolStyles";

interface FakeAck { status: string; value?: unknown; reason?: string }
// GetConfig sends just { key }; SetConfig adds value, asserted at the call site.
type FakeCommandArgs = { key: string; value?: unknown };
function fakeCommands(overrides?: Partial<{ get: FakeAck; set: FakeAck }>) {
  const calls: { name: string; args: FakeCommandArgs }[] = [];
  const sendCommand = vi.fn(async (name: string, args: unknown): Promise<FakeAck> => {
    calls.push({ name, args: args as FakeCommandArgs });
    if (name === "GetConfig") return overrides?.get ?? { status: "accepted", value: {} };
    return overrides?.set ?? { status: "accepted" };
  });
  return { sendCommand, calls };
}

// vi.fn-wrapped async mocks resolve a couple microtask ticks later than a plain
// async function (see store.test.ts's ensureLoaded test for the same note), so
// drain more ticks than a bare `await Promise.resolve()` pair before asserting
// on state set inside the GetConfig .then() handler.
async function settle(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

describe("DrawingToolStyleStore in-memory behavior", () => {
  it("styleFor returns {} for a kind with no remembered style", () => {
    const s = new DrawingToolStyleStore();
    expect(s.styleFor("trendline")).toEqual({});
  });

  it("remember stores a style per kind, independently of other kinds", () => {
    const s = new DrawingToolStyleStore();
    s.remember("trendline", { color: "#2962FF", width: 2, lineStyle: "dashed" });
    s.remember("hline", { color: "#F23645" });
    expect(s.styleFor("trendline")).toEqual({ color: "#2962FF", width: 2, lineStyle: "dashed" });
    expect(s.styleFor("hline")).toEqual({ color: "#F23645" });
    expect(s.styleFor("rect")).toEqual({});
  });

  it("remember merges only the defined fields, leaving previously remembered fields intact", () => {
    const s = new DrawingToolStyleStore();
    s.remember("trendline", { color: "#2962FF", width: 2, lineStyle: "dashed" });
    s.remember("trendline", { color: "#F23645" }); // width/lineStyle omitted, not cleared
    expect(s.styleFor("trendline")).toEqual({ color: "#F23645", width: 2, lineStyle: "dashed" });
  });
});

describe("DrawingToolStyleStore persistence", () => {
  it("connect loads a previously remembered style from GetConfig", async () => {
    const cmd = fakeCommands({ get: { status: "accepted", value: { trendline: { color: "#2962FF", width: 2, lineStyle: "solid" } } } });
    const s = new DrawingToolStyleStore(0);
    s.connect({ commands: cmd });
    await settle();
    expect(s.styleFor("trendline")).toEqual({ color: "#2962FF", width: 2, lineStyle: "solid" });
  });

  it("drops a malformed remembered style on load instead of crashing", async () => {
    const cmd = fakeCommands({ get: { status: "accepted", value: { trendline: { width: "thick" }, hline: { color: "#2962FF" } } } });
    const s = new DrawingToolStyleStore(0);
    s.connect({ commands: cmd });
    await settle();
    expect(s.styleFor("trendline")).toEqual({});
    expect(s.styleFor("hline")).toEqual({ color: "#2962FF" });
  });

  it("remember schedules a debounced SetConfig write of the whole map", async () => {
    const cmd = fakeCommands();
    const s = new DrawingToolStyleStore(0); // debounceMs 0
    s.connect({ commands: cmd });
    await settle(); // let the initial GetConfig settle first
    s.remember("trendline", { color: "#2962FF" });
    await new Promise((resolve) => setTimeout(resolve, 5));
    const set = cmd.calls.find((c) => c.name === "SetConfig");
    expect(set?.args.key).toBe("drawings.toolStyles");
    expect(set?.args.value).toEqual({ trendline: { color: "#2962FF" } });
  });

  it("remember before connect still works in-memory and never persists", async () => {
    const s = new DrawingToolStyleStore(0);
    s.remember("trendline", { color: "#2962FF" });
    expect(s.styleFor("trendline")).toEqual({ color: "#2962FF" });
    await s.flush(); // no throw, no deps
  });
});
