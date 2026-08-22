import { describe, it, expect, vi } from "vitest";
import { WorkspaceStore } from "./workspace";
import { buildMonitoringWorkspace } from "./presets";
import type { WorkspaceApi } from "./workspaceApi";

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
    expect(ws).toEqual({ name: "main", layoutVersion: 8, panels: [], layout: null });
  });

  it("debounces saves into a single config write", async () => {
    vi.useFakeTimers();
    const client = fakeClient();
    const store = new WorkspaceStore(client, 50);
    const ws = { name: "main", layoutVersion: 8, panels: [], layout: null };
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

  it("flush waits for an in-flight save before writing newer state", async () => {
    vi.useFakeTimers();
    try {
      let releaseFirst!: () => void;
      const firstSave = new Promise<void>((resolve) => { releaseFirst = resolve; });
      const first = { name: "main", layoutVersion: 8, panels: [], layout: { first: true } };
      const latest = { ...first, layout: { latest: true } };
      let saves = 0;
      const api: WorkspaceApi = {
        getCatalog: vi.fn(async () => ({ revision: 0, entries: [], openWorkspaceIds: [] })),
        create: vi.fn(), rename: vi.fn(), remove: vi.fn(), open: vi.fn(), focus: vi.fn(), close: vi.fn(),
        load: vi.fn(async (workspaceId) => ({ status: "accepted" as const, workspaceId, revision: 0, document: first })),
        save: vi.fn(async ({ workspaceId, document }) => {
          saves++;
          if (saves === 1) await firstSave;
          return { status: "accepted" as const, workspaceId, revision: saves, document };
        }),
        flush: vi.fn(async () => ({ status: "accepted" as const })),
      };
      const store = new WorkspaceStore({ sendCommand: vi.fn(), workspace: api }, 0);
      store.save(first);
      vi.advanceTimersByTime(1);
      await Promise.resolve();
      expect(api.save).toHaveBeenCalledTimes(1);

      store.save(latest);
      vi.advanceTimersByTime(1);
      expect(api.save).toHaveBeenCalledTimes(1);
      releaseFirst();
      await vi.waitFor(() => expect(api.save).toHaveBeenCalledTimes(2));

      expect(api.save).toHaveBeenLastCalledWith({ workspaceId: "main", document: latest, expectedRevision: 1 });
      await store.flush();
      expect(api.flush).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps pending writes for different workspace identities independent", async () => {
    vi.useFakeTimers();
    try {
      const client = fakeClient();
      const store = new WorkspaceStore(client, 50);
      store.save({ name: "source", layoutVersion: 8, panels: [], layout: null });
      store.save({ name: "monitoring", layoutVersion: 8, panels: [], layout: null });

      await store.flush();

      expect(client.calls.filter((c) => c.name === "SetConfig").map((c) => c.args)).toEqual([
        { key: "workspace.source", value: { name: "source", layoutVersion: 8, panels: [], layout: null } },
        { key: "workspace.monitoring", value: { name: "monitoring", layoutVersion: 8, panels: [], layout: null } },
      ]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("resets an unmarked workspace once and persists the current blank version", async () => {
    let stored: unknown = {
      name: "main", panels: [{ id: "old", panelId: "chart", group: "red", settings: {} }],
      layout: { stale: true }, groups: { red: "US.AAPL" }, linkVenues: { red: "alpaca" },
    };
    const client = {
      sendCommand: vi.fn(async (name: string, args: unknown) => {
        if (name === "GetConfig") return { status: "accepted", value: stored };
        if (name === "SetConfig") { stored = (args as { value: unknown }).value; return { status: "accepted" }; }
        return { status: "accepted" };
      }),
    };
    const store = new WorkspaceStore(client);

    await expect(store.load("main")).resolves.toEqual({ name: "main", layoutVersion: 8, panels: [], layout: null });
    expect(client.sendCommand).toHaveBeenCalledTimes(2);
    expect(stored).toEqual({ name: "main", layoutVersion: 8, panels: [], layout: null });

    await expect(store.load("main")).resolves.toEqual(stored);
    expect(client.sendCommand).toHaveBeenCalledTimes(3);
  });

  it("seeds Monitoring only when its workspace document is missing", async () => {
    const client = fakeClient();
    const seed = buildMonitoringWorkspace();
    const store = new WorkspaceStore(client);

    await expect(store.load("monitoring", seed)).resolves.toBe(seed);
    expect(client.calls).toEqual([
      { name: "GetConfig", args: { key: "workspace.monitoring" } },
      { name: "SetConfig", args: { key: "workspace.monitoring", value: seed } },
    ]);
  });

  it("preserves an existing Monitoring workspace instead of reseeding it", async () => {
    const existing = { ...buildMonitoringWorkspace(), panels: [{ id: "custom", panelId: "chart", group: null, settings: { timeframe: "D" } }] };
    const client = { sendCommand: vi.fn(async (name: string) => name === "GetConfig" ? { status: "accepted", value: existing } : { status: "accepted" }) };
    const store = new WorkspaceStore(client);

    await expect(store.load("monitoring", buildMonitoringWorkspace())).resolves.toBe(existing);
    expect(client.sendCommand).toHaveBeenCalledTimes(1);
  });

  it("seeds through the canonical API and ignores stale stream revisions", async () => {
    const seed = buildMonitoringWorkspace();
    let emit: ((message: { kind: string; topic: string; payload: unknown }) => void) | undefined;
    const api: WorkspaceApi = {
      getCatalog: vi.fn(async () => ({ revision: 0, entries: [], openWorkspaceIds: [] })),
      create: vi.fn(), rename: vi.fn(), remove: vi.fn(), open: vi.fn(), focus: vi.fn(), close: vi.fn(),
      flush: vi.fn(async () => ({ status: "accepted" as const })),
      load: vi.fn(async () => ({ status: "blocked" as const, reason: "workspace document is missing", workspaceId: "monitoring", revision: 0 })),
      save: vi.fn(async ({ workspaceId, document }) => ({ status: "accepted" as const, workspaceId, revision: 1, document })),
    };
    const client = {
      sendCommand: vi.fn(), workspace: api,
      subscribe: vi.fn((_topic: "workspace", listener: (message: { kind: string; topic: string; payload: unknown }) => void) => {
        emit = listener;
        return () => { emit = undefined; };
      }),
    };
    const store = new WorkspaceStore(client);
    const changed = vi.fn();
    store.watch("monitoring", changed);

    await expect(store.load("monitoring", seed)).resolves.toBe(seed);
    expect(client.subscribe).toHaveBeenCalledTimes(1);
    expect(api.save).toHaveBeenCalledWith({ workspaceId: "monitoring", document: seed, expectedRevision: 0 });

    emit?.({ kind: "delta", topic: "workspace", payload: { workspaceId: "monitoring", kind: "document", revision: 1 } });
    emit?.({ kind: "delta", topic: "workspace", payload: { workspaceId: "monitoring", kind: "document", revision: 3 } });
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("refetches four workspace projections when the stream revision gaps", async () => {
    const document = { name: "desk", layoutVersion: 8, panels: [], layout: null };
    let revision = 1;
    const listeners: Array<(message: { kind: string; topic: string; payload: unknown }) => void> = [];
    const api: WorkspaceApi = {
      getCatalog: vi.fn(async () => ({ revision: 1, entries: [{ workspaceId: "desk", name: "Desk", open: true }], openWorkspaceIds: ["desk"] })),
      create: vi.fn(), rename: vi.fn(), remove: vi.fn(), open: vi.fn(), focus: vi.fn(), close: vi.fn(),
      load: vi.fn(async (workspaceId) => ({ status: "accepted" as const, workspaceId, revision, document })),
      save: vi.fn(), flush: vi.fn(async () => ({ status: "accepted" as const })),
    };
    const clients = Array.from({ length: 4 }, () => ({
      sendCommand: vi.fn(),
      workspace: api,
      subscribe: vi.fn((_topic: "workspace", listener: (message: { kind: string; topic: string; payload: unknown }) => void) => {
        listeners.push(listener);
        return () => undefined;
      }),
    }));
    const stores = clients.map((client) => new WorkspaceStore(client));
    await Promise.all(stores.map((store) => store.load("desk")));
    stores.forEach((store) => store.watch("desk", () => { void store.load("desk"); }));

    revision = 3;
    listeners.forEach((listener) => listener({ kind: "delta", topic: "workspace", payload: { workspaceId: "desk", kind: "document", revision } }));
    await vi.waitFor(() => expect(api.load).toHaveBeenCalledTimes(8));
  });
});
