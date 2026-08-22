import { describe, expect, it, vi } from "vitest";
import { completeDurableWorkspaceClose } from "./workspaceClose";
import type { Workspace } from "./workspace";

const document: Workspace = { name: "main", layoutVersion: 8, panels: [], layout: null };

describe("completeDurableWorkspaceClose", () => {
  it("serializes the current document, flushes it, then acknowledges native close", async () => {
    const calls: string[] = [];
    const save = vi.fn((next: Workspace) => { calls.push(`save:${JSON.stringify(next.layout)}`); });
    const flush = vi.fn(async () => { calls.push("flush"); });
    const complete = vi.fn(async () => { calls.push("complete"); return { status: "accepted" as const }; });

    await completeDurableWorkspaceClose({
      workspaceId: "main",
      requestId: "close-1",
      getCurrentDocument: () => ({ ...document, layout: { grid: "latest" } }),
      save,
      flush,
      complete,
    });

    expect(calls).toEqual(["save:{\"grid\":\"latest\"}", "flush", "complete"]);
    expect(complete).toHaveBeenCalledWith({ workspaceId: "main", requestId: "close-1" });
  });

  it("does not acknowledge when the durable flush fails", async () => {
    const complete = vi.fn();
    await expect(completeDurableWorkspaceClose({
      workspaceId: "main",
      requestId: "close-1",
      getCurrentDocument: () => document,
      save: vi.fn(),
      flush: vi.fn(async () => { throw new Error("storage busy"); }),
      complete,
    })).rejects.toThrow("storage busy");
    expect(complete).not.toHaveBeenCalled();
  });
});
