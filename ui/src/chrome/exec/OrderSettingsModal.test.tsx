// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import { OrderSettingsModal } from "./OrderSettingsModal";
import { DEFAULT_ORDER_CONFIG } from "./actionTemplate";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import { SoundConfigProvider } from "../../sound/SoundConfigProvider";
import type { AckMsg, ExecStatus } from "../../wire/contract";

const status: ExecStatus = { masterArmed: true, global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }] };

const soundCommands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined }) as AckMsg) };

function wrap(onSave = vi.fn(), onClose = vi.fn()) {
  render(
    <ThemeProvider>
      <SoundConfigProvider commands={soundCommands as never}>
        <OrderSettingsModal config={DEFAULT_ORDER_CONFIG} status={status} onSave={onSave} onClose={onClose} />
      </SoundConfigProvider>
    </ThemeProvider>,
  );
  return { onSave, onClose };
}

describe("OrderSettingsModal", () => {
  it("lists templates and saves an edited label", () => {
    const { onSave } = wrap();
    const label = screen.getByTestId("tmpl-label-buy-5k") as HTMLInputElement;
    fireEvent.change(label, { target: { value: "Buy big" } });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").label).toBe("Buy big");
  });
  it("captures a hotkey combo from a keypress", () => {
    const { onSave } = wrap();
    const cap = screen.getByTestId("tmpl-hotkey-buy-5k");
    fireEvent.keyDown(cap, { key: "7", ctrlKey: true, altKey: true });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").hotkey).toBe("Ctrl+Alt+7");
  });
  it("adds and removes a template", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].templates.length).toBe(DEFAULT_ORDER_CONFIG.templates.length + 1);
  });
  it("shows the active gate caps read-only (0 → off)", () => {
    wrap();
    expect(screen.getByText(/alpaca-paper/)).toBeTruthy();
    expect(screen.getByText(/max order value/i).textContent).toMatch(/1000/);
    expect(screen.getByText(/max position value/i).textContent).toMatch(/off/i);
  });
  it("renders the Sounds section", () => {
    wrap();
    expect(screen.getByTestId("sound-fill")).toBeTruthy();
  });

  // Regression for a CRITICAL safety finding: the capture input previously called
  // only e.preventDefault(), not e.stopPropagation(). The real hotkey engine
  // (useHotkeys) listens for keydown on `window` in the bubble phase, so a
  // candidate combo typed while capturing a NEW binding could also be a *live*
  // combo already bound to a real template (e.g. default Ctrl+Shift+K =
  // KillSwitch) and fire the real action from what must be an inert settings
  // screen. This mounts the real useHotkeys engine (not a fake) alongside the
  // modal, proving the leak path is actually closed end-to-end.
  it("does not leak a captured keydown to the global hotkey engine (KillSwitch stays inert)", async () => {
    const stores = makeStores();
    const sent: Array<{ name: string; args: unknown }> = [];
    const commands = {
      sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => {
        sent.push({ name: n, args: a });
        return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
      }),
    };
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status });

    function Harness() {
      useHotkeys({ stores, commands, linkGroups, group: "green" });
      return (
        <SoundConfigProvider commands={soundCommands as never}>
          <OrderSettingsModal config={DEFAULT_ORDER_CONFIG} status={status} onSave={vi.fn()} onClose={vi.fn()} />
        </SoundConfigProvider>
      );
    }

    await act(async () => {
      render(
        <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
          <Harness />
        </OrderConfigProvider></ToastProvider></ThemeProvider>,
      );
      await Promise.resolve();
    });

    // buy-5k's default hotkey is Ctrl+1; capture a combo on it that happens to
    // collide with the live, already-bound KillSwitch combo (Ctrl+Shift+K).
    const cap = screen.getByTestId("tmpl-hotkey-buy-5k") as HTMLInputElement;
    await act(async () => {
      fireEvent.keyDown(cap, { key: "k", ctrlKey: true, shiftKey: true });
      await Promise.resolve();
    });

    // The capture input took the new binding...
    expect(cap.value).toBe("Ctrl+Shift+K");
    // ...but the real KillSwitch command must NOT have fired on the global engine.
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(false);
  });
});
