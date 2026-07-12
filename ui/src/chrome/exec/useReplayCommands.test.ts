// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useReplayCommands, type ReplayCommandAdapter } from "./useReplayCommands";
import type { AckMsg } from "../../wire/contract";

function fakeAdapter() {
  const sent: Array<{ name: string; args: unknown }> = [];
  const adapter: ReplayCommandAdapter = {
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      sent.push({ name, args });
      return { kind: "ack", corrId: "c1", status: "accepted" };
    }),
    sendQuery: vi.fn(async () => ["2026-01-02", "2026-01-05"]),
  };
  return { sent, adapter };
}

describe("useReplayCommands", () => {
  it("start sends StartReplay with day + speed", async () => {
    const { sent, adapter } = fakeAdapter();
    const { result } = renderHook(() => useReplayCommands(adapter));
    await result.current.start("2026-01-02", 4);
    expect(sent).toEqual([{ name: "StartReplay", args: { day: "2026-01-02", speed: 4 } }]);
  });

  it("goLive sends GoLive with empty args", async () => {
    const { sent, adapter } = fakeAdapter();
    const { result } = renderHook(() => useReplayCommands(adapter));
    await result.current.goLive();
    expect(sent).toEqual([{ name: "GoLive", args: {} }]);
  });

  it("listDays queries ListReplayDays and returns the day list", async () => {
    const { adapter } = fakeAdapter();
    const { result } = renderHook(() => useReplayCommands(adapter));
    await expect(result.current.listDays()).resolves.toEqual(["2026-01-02", "2026-01-05"]);
  });

  // Task 5 (U3): the new synthetic-demo-market entry point.
  it("startDemo sends StartDemo with empty args", async () => {
    const { sent, adapter } = fakeAdapter();
    const { result } = renderHook(() => useReplayCommands(adapter));
    const ack = await result.current.startDemo();
    expect(sent).toEqual([{ name: "StartDemo", args: {} }]);
    expect(ack.status).toBe("accepted");
  });
});
