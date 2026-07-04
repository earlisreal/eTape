import { describe, it, expect, vi } from "vitest";
import { WorkspaceStore } from "./workspace";
import { SEED_WORKSPACES } from "../seeds/workspaces";

function fakeClient() {
  const calls: Array<{ name: string; args: unknown }> = [];
  return {
    calls,
    sendCommand: vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { status: "accepted" }; }),
  };
}

describe("WorkspaceStore", () => {
  it("falls back to the seed when no saved doc exists", async () => {
    const client = fakeClient();
    // getConfig returns null (nothing saved yet)
    client.sendCommand.mockImplementationOnce(async () => ({ status: "accepted", value: null }));
    const store = new WorkspaceStore(client, 10);
    const ws = await store.load("monitoring");
    expect(ws.name).toBe("Monitoring");
    expect(ws.panels.length).toBe(SEED_WORKSPACES.monitoring.panels.length);
  });

  it("debounces saves into a single config write", async () => {
    vi.useFakeTimers();
    const client = fakeClient();
    const store = new WorkspaceStore(client, 50);
    store.save({ ...SEED_WORKSPACES.trading });
    store.save({ ...SEED_WORKSPACES.trading });
    store.save({ ...SEED_WORKSPACES.trading });
    expect(client.calls.filter((c) => c.name === "SetConfig")).toHaveLength(0);
    vi.advanceTimersByTime(60);
    await store.flush();
    expect(client.calls.filter((c) => c.name === "SetConfig")).toHaveLength(1);
    vi.useRealTimers();
  });
});
