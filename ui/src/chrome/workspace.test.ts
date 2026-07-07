import { describe, it, expect, vi } from "vitest";
import { WorkspaceStore } from "./workspace";

function fakeClient() {
  const calls: Array<{ name: string; args: unknown }> = [];
  return {
    calls,
    sendCommand: vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { status: "accepted" }; }),
  };
}

describe("WorkspaceStore", () => {
  it("returns a blank workspace when no doc is saved", async () => {
    const client = { sendCommand: vi.fn().mockResolvedValue({ status: "accepted", value: null }) };
    const store = new WorkspaceStore(client);
    const ws = await store.load("main");
    expect(ws).toEqual({ name: "main", panels: [], layout: null });
  });

  it("debounces saves into a single config write", async () => {
    vi.useFakeTimers();
    const client = fakeClient();
    const store = new WorkspaceStore(client, 50);
    const ws = { name: "main", panels: [], layout: null };
    store.save({ ...ws });
    store.save({ ...ws });
    store.save({ ...ws });
    expect(client.calls.filter((c) => c.name === "SetConfig")).toHaveLength(0);
    vi.advanceTimersByTime(60);
    await store.flush();
    const setConfigCalls = client.calls.filter((c) => c.name === "SetConfig");
    expect(setConfigCalls).toHaveLength(1);
    expect(setConfigCalls[0].args).toEqual({ key: "workspace.main", value: ws });
    vi.useRealTimers();
  });
});
