// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import { OrderSettingsSection } from "./OrderSettingsSection";
import { DEFAULT_ORDER_CONFIG, normalizeOrderConfig } from "./actionTemplate";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus } from "../../wire/contract";

// Seeds the hotkeys engine's exec store in the kill-leak regression below —
// unrelated to OrderSettingsSection's own props (it no longer takes `status`).
const status: ExecStatus = { masterArmed: true, global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }] };

function wrap(onSave = vi.fn()) {
  render(
    <ThemeProvider>
      <OrderSettingsSection config={DEFAULT_ORDER_CONFIG} onSave={onSave} />
    </ThemeProvider>,
  );
  return { onSave };
}

describe("OrderSettingsSection", () => {
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

  it("edits price offset value and unit and round-trips both on save", () => {
    const { onSave } = wrap();
    fireEvent.change(screen.getByLabelText("offset-buy-5k"), { target: { value: "0.05" } });
    fireEvent.change(screen.getByLabelText("offset-unit-buy-5k"), { target: { value: "%" } });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    const tmpl = saved.templates.find((t: { id: string }) => t.id === "buy-5k");
    expect(tmpl.priceOffset).toBe(0.05);
    expect(tmpl.priceOffsetUnit).toBe("%");
  });

  it("switches sizing mode then edits the sizing amount, round-tripping both on save", () => {
    const { onSave } = wrap();
    // buy-5k starts as Dollar/5000; switch to PositionFraction (defaults pct to 100)...
    fireEvent.change(screen.getByLabelText("size-mode-buy-5k"), { target: { value: "PositionFraction" } });
    // ...then dial the amount down to 50%.
    fireEvent.change(screen.getByLabelText("size-value-buy-5k"), { target: { value: "50" } });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    const tmpl = saved.templates.find((t: { id: string }) => t.id === "buy-5k");
    expect(tmpl.sizing).toEqual({ mode: "PositionFraction", pct: 50 });
  });

  it("add-place creates a place-kind template", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-place"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.length).toBe(DEFAULT_ORDER_CONFIG.templates.length + 1);
    expect(saved.templates[saved.templates.length - 1].kind).toBe("place");
  });

  it("add-manage creates a manage-kind template", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-manage"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.length).toBe(DEFAULT_ORDER_CONFIG.templates.length + 1);
    expect(saved.templates[saved.templates.length - 1].kind).toBe("manage");
  });

  it("unbinds a hotkey via tmpl-unbind-*", () => {
    const { onSave } = wrap();
    // buy-5k ships with a default hotkey, so the unbind (×) button is present.
    fireEvent.click(screen.getByTestId("tmpl-unbind-buy-5k"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").hotkey).toBe("");
  });

  it("reset-defaults then reset-confirm restores DEFAULT_TEMPLATES", () => {
    const { onSave } = wrap();
    // Mutate first, to prove the reset actually discards the edit rather than
    // happening to already match defaults.
    fireEvent.change(screen.getByTestId("tmpl-label-buy-5k"), { target: { value: "mutated" } });
    fireEvent.click(screen.getByTestId("reset-defaults"));
    fireEvent.click(screen.getByTestId("reset-confirm"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates).toEqual(normalizeOrderConfig(DEFAULT_ORDER_CONFIG).templates);
  });

  it("disables save on a duplicate hotkey binding and re-enables it once unbound", () => {
    wrap();
    // duplicate binding disables save (plain DOM property — jest-dom is not installed)
    const save = () => screen.getByTestId("save") as HTMLButtonElement;
    expect(save().disabled).toBe(false);
    fireEvent.keyDown(screen.getByTestId("tmpl-hotkey-buy-25pct"), { key: "1", ctrlKey: true });
    expect(save().disabled).toBe(true);
    fireEvent.click(screen.getByTestId("tmpl-unbind-buy-25pct"));
    expect(save().disabled).toBe(false);
  });

  it("renders the cheat-sheet strip with bound template labels and reflects a live label edit", () => {
    wrap();
    const sheet = screen.getByTestId("cheat-sheet");
    expect(sheet.textContent).toContain("Buy $5k");
    fireEvent.change(screen.getByTestId("tmpl-label-buy-5k"), { target: { value: "Big buy" } });
    expect(sheet.textContent).toContain("Big buy");
    expect(sheet.textContent).not.toContain("Buy $5k");
  });

  // Regression for a CRITICAL safety finding: the capture input previously called
  // only e.preventDefault(), not e.stopPropagation(). The real hotkey engine
  // (useHotkeys) listens for keydown on `window` in the bubble phase, so a
  // candidate combo typed while capturing a NEW binding could also be a *live*
  // combo already bound to a real template (e.g. default Ctrl+Shift+K =
  // KillSwitch) and fire the real action from what must be an inert settings
  // screen. This mounts the real useHotkeys engine (not a fake) alongside the
  // section, proving the leak path is actually closed end-to-end.
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
      return <OrderSettingsSection config={DEFAULT_ORDER_CONFIG} onSave={vi.fn()} />;
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
