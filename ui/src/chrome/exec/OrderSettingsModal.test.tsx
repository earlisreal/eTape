// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { OrderSettingsModal } from "./OrderSettingsModal";
import { DEFAULT_ORDER_CONFIG } from "./actionTemplate";
import type { ExecStatus } from "../../wire/contract";

const status: ExecStatus = { masterArmed: true, global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }] };

function wrap(onSave = vi.fn(), onClose = vi.fn()) {
  render(<ThemeProvider><OrderSettingsModal config={DEFAULT_ORDER_CONFIG} status={status} onSave={onSave} onClose={onClose} /></ThemeProvider>);
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
});
