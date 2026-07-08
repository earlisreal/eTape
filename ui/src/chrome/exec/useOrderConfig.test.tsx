// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { OrderConfigProvider, useOrderConfig } from "./useOrderConfig";
import { DEFAULT_ORDER_CONFIG, normalizeOrderConfig, type OrderConfig } from "./actionTemplate";
import type { AckMsg } from "../../wire/contract";

function cmds(getValue?: unknown) {
  const calls: Array<{ name: string; args: unknown }> = [];
  return {
    calls,
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      calls.push({ name, args });
      if (name === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: getValue };
      return { kind: "ack", corrId: "c", status: "accepted" };
    }),
  };
}
const wrapper = (c: ReturnType<typeof cmds>) => ({ children }: { children: ReactNode }) =>
  <OrderConfigProvider commands={c}>{children}</OrderConfigProvider>;

describe("useOrderConfig", () => {
  it("falls back to defaults when the store has no value", async () => {
    const c = cmds(undefined);
    const { result } = renderHook(() => useOrderConfig(), { wrapper: wrapper(c) });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(result.current.config).toEqual(normalizeOrderConfig(DEFAULT_ORDER_CONFIG));
  });
  it("loads a persisted config, and setActiveVenue persists via SetConfig", async () => {
    const persisted: OrderConfig = { templates: [], activeVenue: "alpaca-paper" };
    const c = cmds(persisted);
    const { result } = renderHook(() => useOrderConfig(), { wrapper: wrapper(c) });
    await waitFor(() => expect(result.current.config.activeVenue).toBe("alpaca-paper"));
    act(() => result.current.setActiveVenue("tradezero-live"));
    expect(result.current.config.activeVenue).toBe("tradezero-live");
    const set = c.calls.find((x) => x.name === "SetConfig");
    expect(set?.args).toMatchObject({ key: "orderConfig" });
  });
});
