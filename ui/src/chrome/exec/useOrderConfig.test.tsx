// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, renderHook, act, fireEvent, screen, waitFor } from "@testing-library/react";
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

function Probe({ id }: { id: string }) {
  const { config, loaded, save } = useOrderConfig();
  return <><span data-testid={`loaded-${id}`}>{String(loaded)}</span><span data-testid={`venue-${id}`}>{config.activeVenue}</span><button data-testid={`save-${id}`} onClick={() => save({ ...config, activeVenue: "alpaca-paper" })}>save</button></>;
}

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

  it("reloads order settings changed by another window", async () => {
    let stored: unknown = { templates: [], activeVenue: "" };
    const makeCommands = () => ({
      sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
        if (name === "SetConfig") stored = (args as { value: unknown }).value;
        return { kind: "ack", corrId: "c", status: "accepted", value: name === "GetConfig" ? stored : undefined };
      }),
    });
    const first = makeCommands();
    const second = makeCommands();
    render(
      <>
        <OrderConfigProvider commands={first}><Probe id="one" /></OrderConfigProvider>
        <OrderConfigProvider commands={second}><Probe id="two" /></OrderConfigProvider>
      </>,
    );
    await waitFor(() => expect(screen.getByTestId("loaded-two").textContent).toBe("true"));
    fireEvent.click(screen.getByTestId("save-one"));
    await waitFor(() => expect(screen.getByTestId("venue-two").textContent).toBe("alpaca-paper"));
  });
});
