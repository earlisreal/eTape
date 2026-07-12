// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import { OrderSettingsSection } from "./OrderSettingsSection";
import { DEFAULT_ORDER_CONFIG, normalizeOrderConfig, type OrderConfig } from "./actionTemplate";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus } from "../../wire/contract";

// Seeds the hotkeys engine's exec store in the kill-leak regression below —
// unrelated to OrderSettingsSection's own props (it no longer takes `status`).
const status: ExecStatus = { masterArmed: true, global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }] };

// This suite exercises the editor's behavior (labels, hotkeys, sizing,
// add/remove, reset), not eTape's actual defaults — DEFAULT_ORDER_CONFIG now
// ships blank (no default templates/hotkeys), so seed a local fixture that
// mirrors the *former* seeded set to keep every existing `buy-5k` / `sell-half`
// / uid-math selector meaningful.
const SAMPLE_ORDER_CONFIG: OrderConfig = {
  activeVenue: "",
  templates: [
    { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
    { kind: "place", id: "buy-25pct", label: "Buy 25% BP", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "BuyingPowerPct", pct: 25 }, hotkey: "Ctrl+2" },
    { kind: "place", id: "sell-half", label: "Sell ½", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", pct: 50 }, hotkey: "Ctrl+3" },
    { kind: "place", id: "flatten", label: "Flatten", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", pct: 100 }, hotkey: "Ctrl+4" },
    { kind: "manage", id: "cancel-last", label: "Cancel Last", action: "CancelLast", hotkey: "Ctrl+Backspace" },
    { kind: "manage", id: "cancel-all", label: "Cancel All (focused)", action: "CancelAllFocused", hotkey: "Ctrl+Shift+Backspace" },
    { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
  ],
};

function wrap(onSave = vi.fn()) {
  const { rerender } = render(
    <ThemeProvider>
      <OrderSettingsSection config={SAMPLE_ORDER_CONFIG} onSave={onSave} />
    </ThemeProvider>,
  );
  return { onSave, rerender };
}

function wrapWithToastAndClose(onSave = vi.fn(), toast = { push: vi.fn(), dismiss: vi.fn() }, onClose = vi.fn()) {
  render(
    <ThemeProvider>
      <OrderSettingsSection config={SAMPLE_ORDER_CONFIG} onSave={onSave} toast={toast} onClose={onClose} />
    </ThemeProvider>,
  );
  return { onSave, toast, onClose };
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

  it("nudges the offset up by 0.05 via the stepper button", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("offset-buy-5k-up"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").priceOffset).toBe(0.05);
  });

  it("nudges the offset below zero via the stepper button (negative offsets allowed)", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("offset-buy-5k-down"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").priceOffset).toBe(-0.05);
  });

  it("nudges the size value per sizing mode (Dollar steps by 100)", () => {
    const { onSave } = wrap();
    // buy-5k starts Dollar/5000.
    fireEvent.click(screen.getByTestId("size-value-buy-5k-up"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").sizing).toEqual({ mode: "Dollar", dollar: 5100 });
  });

  it("caps Buying-power % sizing at 100 when nudged up repeatedly past the cap", () => {
    const { onSave } = wrap();
    // buy-25pct starts BuyingPowerPct/25; 80 clicks of +1 would reach 105 uncapped.
    const up = screen.getByTestId("size-value-buy-25pct-up");
    for (let i = 0; i < 80; i++) fireEvent.click(up);
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-25pct").sizing).toEqual({ mode: "BuyingPowerPct", pct: 100 });
  });

  it("caps a typed Position % sizing value at 100 on blur, but shows the raw typed text while editing", () => {
    const { onSave } = wrap();
    // sell-half starts PositionFraction/50.
    const sizeValue = screen.getByLabelText("size-value-sell-half") as HTMLInputElement;
    fireEvent.change(sizeValue, { target: { value: "150" } });
    expect(sizeValue.value).toBe("150");
    fireEvent.blur(sizeValue);
    expect(sizeValue.value).toBe("100");
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "sell-half").sizing).toEqual({ mode: "PositionFraction", pct: 100 });
  });

  it("shares sizing floors a nudge to a whole share (no upper cap)", () => {
    const { onSave } = wrap();
    fireEvent.change(screen.getByLabelText("size-mode-buy-5k"), { target: { value: "Shares" } });
    fireEvent.click(screen.getByTestId("size-value-buy-5k-up")); // 100 default -> 101
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").sizing).toEqual({ mode: "Shares", shares: 101 });
  });

  // Regression: e.key for Shift+1 is the shifted glyph "!" (what the browser
  // actually produced), not the digit — normalizeCombo must prefer e.code
  // ("Digit1") so the captured combo reads "Shift+1", not "Shift+!".
  it("captures Shift+1 as \"Shift+1\", not \"Shift+!\"", () => {
    const { onSave } = wrap();
    const cap = screen.getByTestId("tmpl-hotkey-buy-5k") as HTMLInputElement;
    fireEvent.keyDown(cap, { key: "!", code: "Digit1", shiftKey: true });
    expect(cap.value).toBe("Shift+1");
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").hotkey).toBe("Shift+1");
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

  // Regression: both numeric cells are fully-controlled inputs whose displayed
  // value is re-derived from the numeric model on every render. A raw
  // fireEvent.change with the complete final string (as in the round-trip
  // test above) always coerces cleanly and never exposed this — only a
  // character-by-character sequence does, because an in-progress value like
  // "0." parses to a valid (non-NaN) 0 and previously got written straight
  // back into the model, so the very next render clobbered the trailing "."
  // the user had just typed, before they could type the next digit.
  it("allows typing a decimal offset value keystroke-by-keystroke without collapsing the trailing digits", () => {
    const { onSave } = wrap();
    const offset = screen.getByLabelText("offset-buy-5k") as HTMLInputElement;
    fireEvent.change(offset, { target: { value: "0" } });
    expect(offset.value).toBe("0");
    fireEvent.change(offset, { target: { value: "0." } });
    expect(offset.value).toBe("0.");
    fireEvent.change(offset, { target: { value: "0.0" } });
    expect(offset.value).toBe("0.0");
    fireEvent.change(offset, { target: { value: "0.05" } });
    expect(offset.value).toBe("0.05");
    fireEvent.blur(offset);
    // Blur clears the raw edit, so the display re-derives from the numeric
    // model through fmtOffset — a positive value now reads "+0.05".
    expect(offset.value).toBe("+0.05");
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").priceOffset).toBe(0.05);
  });

  it("allows typing a decimal size-value keystroke-by-keystroke without collapsing the trailing digits", () => {
    const { onSave } = wrap();
    // sell-half starts as PositionFraction/50 — the %-unit case where
    // fractional offsets/sizes are the common case.
    const sizeValue = screen.getByLabelText("size-value-sell-half") as HTMLInputElement;
    fireEvent.change(sizeValue, { target: { value: "5" } });
    expect(sizeValue.value).toBe("5");
    fireEvent.change(sizeValue, { target: { value: "50" } });
    expect(sizeValue.value).toBe("50");
    fireEvent.change(sizeValue, { target: { value: "50." } });
    expect(sizeValue.value).toBe("50.");
    fireEvent.change(sizeValue, { target: { value: "50.5" } });
    expect(sizeValue.value).toBe("50.5");
    fireEvent.blur(sizeValue);
    expect(sizeValue.value).toBe("50.5");
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "sell-half").sizing).toEqual({ mode: "PositionFraction", pct: 50.5 });
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
    expect(saved.templates.length).toBe(SAMPLE_ORDER_CONFIG.templates.length + 1);
    expect(saved.templates[saved.templates.length - 1].kind).toBe("place");
    expect(saved.templates[saved.templates.length - 1].session).toBe("AUTO");
  });

  it("edits a template's session and round-trips it on save", () => {
    const { onSave } = wrap();
    // buy-5k starts at the default session ("AUTO" — normalizeOrderConfig
    // fills it in for SAMPLE_ORDER_CONFIG, which doesn't set it explicitly).
    expect((screen.getByLabelText("session-buy-5k") as HTMLSelectElement).value).toBe("AUTO");
    fireEvent.change(screen.getByLabelText("session-buy-5k"), { target: { value: "OVERNIGHT" } });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").session).toBe("OVERNIGHT");
  });

  it("add-manage creates a manage-kind template", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-manage"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.length).toBe(SAMPLE_ORDER_CONFIG.templates.length + 1);
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

  // Regression: uid() is deterministic in templates.length alone, so once the
  // array returns to a prior length (add-then-remove), the next add reuses
  // the exact same id. removeTemplate must drop that id's rawEdits entries,
  // or a still-in-progress (unblurred) edit on the removed row leaks onto
  // whichever new row is assigned the reused id — a WYSIWYG desync where the
  // input shows stale typed text but the saved model holds the real value.
  it("clears a stale raw-edit override when a template is removed and its id is reused", () => {
    const { onSave } = wrap();

    // SAMPLE_ORDER_CONFIG has 7 entries (indices 0..6), so the first add
    // deterministically mints id "tmpl-8-7".
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-place"));
    const firstOffset = screen.getByLabelText("offset-tmpl-8-7") as HTMLInputElement;
    expect(firstOffset.value).toBe("0");

    // Start editing but never blur — mirrors a stray click removing the row
    // (e.g. Safari, where a non-text button click doesn't blur a sibling
    // input first) that leaves rawEdits["tmpl-8-7:offset"] live.
    fireEvent.change(firstOffset, { target: { value: "0." } });
    expect(firstOffset.value).toBe("0.");

    // Remove the row via its own "x" button without blurring the input first.
    const removeButtons = screen.getAllByTitle("remove");
    fireEvent.click(removeButtons[removeButtons.length - 1]);

    // Adding again reuses the exact same id, since templates.length is back
    // to 7.
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-place"));
    const reusedOffset = screen.getByLabelText("offset-tmpl-8-7") as HTMLInputElement;

    // Must show the new row's own default (0), not the "0." leftover from
    // the removed row that happened to reuse the same id.
    expect(reusedOffset.value).toBe("0");

    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "tmpl-8-7").priceOffset).toBe(0);
  });

  // Reset replaces every template wholesale (eTape ships with NO default
  // templates/hotkeys, so reset-to-defaults clears the list entirely) — an
  // in-progress raw edit for a row that gets wiped by the reset must not
  // leak into whatever renders afterward.
  it("clears a stale raw-edit override across reset-to-defaults", () => {
    const { onSave } = wrap();
    const offset = screen.getByLabelText("offset-buy-5k") as HTMLInputElement;

    // In-progress edit, never blurred.
    fireEvent.change(offset, { target: { value: "1." } });
    expect(offset.value).toBe("1.");

    fireEvent.click(screen.getByTestId("reset-defaults"));
    fireEvent.click(screen.getByTestId("reset-defaults")); // same button, now armed

    // The default set is empty, so buy-5k's card (and its in-progress "1."
    // edit) is gone entirely rather than snapping back to some value.
    expect(screen.queryByTestId("tmpl-card-buy-5k")).toBeNull();

    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates).toEqual([]);
  });

  it("reset-defaults then reset-confirm clears every template (defaults are blank)", () => {
    const { onSave } = wrap();
    // Mutate first, to prove the reset actually discards the edit rather than
    // happening to already match defaults.
    fireEvent.change(screen.getByTestId("tmpl-label-buy-5k"), { target: { value: "mutated" } });
    fireEvent.click(screen.getByTestId("reset-defaults"));
    fireEvent.click(screen.getByTestId("reset-defaults")); // same button, now armed
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

  // Regression: Task 6 co-mounted this section with the hotkeys BackupPanel in
  // the same "orders" settings pane, sharing one OrderConfig context. Before
  // that, importing hotkeys and viewing "Orders & hotkeys" were mutually
  // exclusive nav sections, so switching to this one always remounted it
  // against the post-import config. Now the component stays mounted while an
  // import happens below it, so it must resync its local `templates` (what
  // the cheat sheet and cards render from) from a new `config` prop without
  // needing to unmount/remount. This mirrors prepareImportedOrderConfig's
  // replace-not-merge behavior: the imported config carries a wholly
  // different template list with freshly-minted ids.
  it("resyncs the cheat sheet when config.templates changes to a new reference without unmounting (hotkey import while mounted)", () => {
    const { onSave, rerender } = wrap();
    const sheet = screen.getByTestId("cheat-sheet");
    expect(sheet.textContent).toContain("Buy $5k");

    const imported: OrderConfig = {
      ...SAMPLE_ORDER_CONFIG,
      templates: [
        {
          kind: "place", id: "imported-buy", label: "Imported Buy", side: "BUY", type: "LIMIT", tif: "DAY",
          priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 1000 }, hotkey: "Ctrl+9",
        },
      ],
    };
    rerender(
      <ThemeProvider>
        <OrderSettingsSection config={imported} onSave={onSave} />
      </ThemeProvider>,
    );

    expect(screen.getByTestId("cheat-sheet").textContent).toContain("Imported Buy");
    expect(screen.getByTestId("cheat-sheet").textContent).not.toContain("Buy $5k");
    expect(screen.queryByTestId("tmpl-card-buy-5k")).toBeNull();
    expect(screen.getByTestId("tmpl-card-imported-buy")).toBeTruthy();
  });

  // Regression for a review finding on the resync effect above: the effect is
  // keyed on `[config.templates]`, not `[config]`, specifically because a
  // sibling writer of the shared `OrderConfig` context — `setActiveVenue`
  // (useOrderConfig.tsx), reachable from venue-selection UI in dockview
  // panels that stay mounted underneath the Settings modal — can change
  // `config` for reasons unrelated to templates while this component is
  // mounted. `setActiveVenue` builds its next config as
  // `{ ...c, activeVenue: v }`, a shallow spread that reuses the same
  // `templates` array reference, so this simulates that exact shape (same
  // reference, different `activeVenue`) and asserts an in-progress, not-yet-
  // saved local edit survives. The edit is an added template with a label
  // ("New") that does not exist anywhere in `config.templates` — if the
  // effect wrongly fired and resynced from `config.templates`, this
  // assertion would fail because the added template would vanish. If a
  // future change to `setActiveVenue` (or a new `OrderConfig` writer) ever
  // mints a new `templates` reference for a templates-unrelated change, this
  // test catches the regression.
  it("keeps an unsaved added template across a venue-only config change (same templates reference)", () => {
    const { rerender } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("add-place"));
    expect(screen.getByDisplayValue("New")).toBeTruthy();

    const venueChanged: OrderConfig = { ...SAMPLE_ORDER_CONFIG, activeVenue: "tradezero" };
    expect(venueChanged.templates).toBe(SAMPLE_ORDER_CONFIG.templates);
    expect(venueChanged.activeVenue).not.toBe(SAMPLE_ORDER_CONFIG.activeVenue);

    rerender(
      <ThemeProvider>
        <OrderSettingsSection config={venueChanged} onSave={vi.fn()} />
      </ThemeProvider>,
    );

    expect(screen.getByDisplayValue("New")).toBeTruthy();
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
        // DEFAULT_ORDER_CONFIG now ships blank, so seed the shared
        // OrderConfigProvider context with SAMPLE_ORDER_CONFIG on its
        // GetConfig read — the real hotkey engine below (useHotkeys) reads
        // config from that shared context, not from OrderSettingsSection's
        // own `config` prop, and this test's whole premise is that
        // Ctrl+Shift+K is a genuinely live KillSwitch binding for it to leak.
        if (n === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: SAMPLE_ORDER_CONFIG };
        return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
      }),
    };
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status });

    function Harness() {
      useHotkeys({ stores, commands, linkGroups, group: "green" });
      return <OrderSettingsSection config={SAMPLE_ORDER_CONFIG} onSave={vi.fn()} />;
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

  // Task 4b: the deck toggle patches `deck` and it survives Save.
  it("toggles the deck checkbox on and off, and it round-trips on save", () => {
    const { onSave } = wrap();
    const toggle = screen.getByTestId("tmpl-deck-toggle-buy-5k") as HTMLInputElement;
    expect(toggle.checked).toBe(false);
    fireEvent.click(toggle);
    expect(toggle.checked).toBe(true);
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").deck).toBe(true);
  });

  // Applies to management templates too (e.g. KillSwitch is a valid deck button).
  it("shows the deck toggle on a management card as well", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("tmpl-deck-toggle-kill"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "kill").deck).toBe(true);
  });

  // Task 4b: color swatches only appear once the deck toggle is on, clicking
  // one sets deckColor, and it survives Save.
  it("sets deckColor via a swatch click once the deck toggle is on, and it round-trips on save", () => {
    const { onSave } = wrap();
    expect(screen.queryByTestId("tmpl-deck-color-buy-5k-green")).toBeNull();
    fireEvent.click(screen.getByTestId("tmpl-deck-toggle-buy-5k"));
    fireEvent.click(screen.getByTestId("tmpl-deck-color-buy-5k-green"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").deckColor).toBe("green");
  });

  // The selected swatch must be visually distinguished (boxShadow ring) so a
  // test can assert selection state, not just click behavior.
  it("visually marks the selected swatch with a boxShadow ring, moving the ring on click", () => {
    wrap();
    fireEvent.click(screen.getByTestId("tmpl-deck-toggle-buy-5k"));
    // Default deckColor is absent -> "auto" is the selected swatch.
    const auto = screen.getByTestId("tmpl-deck-color-buy-5k-auto") as HTMLButtonElement;
    const green = screen.getByTestId("tmpl-deck-color-buy-5k-green") as HTMLButtonElement;
    expect(auto.style.boxShadow).not.toBe("none");
    expect(green.style.boxShadow).toBe("none");
    fireEvent.click(green);
    expect(green.style.boxShadow).not.toBe("none");
    expect(auto.style.boxShadow).toBe("none");
  });

  // Task 4a: reorder swaps two templates and the swap is reflected on save.
  it("moves a template down and its neighbor up, swapping their array positions on save", () => {
    const { onSave } = wrap();
    // buy-5k (index 0) and buy-25pct (index 1) are adjacent.
    fireEvent.click(screen.getByTestId("tmpl-move-down-buy-5k"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates[0].id).toBe("buy-25pct");
    expect(saved.templates[1].id).toBe("buy-5k");
  });

  it("moves a template up, swapping it with its predecessor on save", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("tmpl-move-up-buy-25pct"));
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates[0].id).toBe("buy-25pct");
    expect(saved.templates[1].id).toBe("buy-5k");
  });

  // Bounds: the first template's move-up and the last template's move-down
  // are disabled (assert the `disabled` attribute, not click-then-no-op).
  it("disables move-up on the first template and move-down on the last template", () => {
    wrap();
    // SAMPLE_ORDER_CONFIG order: buy-5k (first) ... kill (last).
    const firstUp = screen.getByTestId("tmpl-move-up-buy-5k") as HTMLButtonElement;
    const firstDown = screen.getByTestId("tmpl-move-down-buy-5k") as HTMLButtonElement;
    const lastUp = screen.getByTestId("tmpl-move-up-kill") as HTMLButtonElement;
    const lastDown = screen.getByTestId("tmpl-move-down-kill") as HTMLButtonElement;
    expect(firstUp.disabled).toBe(true);
    expect(firstDown.disabled).toBe(false);
    expect(lastUp.disabled).toBe(false);
    expect(lastDown.disabled).toBe(true);
  });

  // Ext-hours buffer % moved to GeneralSection.tsx; this component must
  // neither render it nor overwrite it on save — Save spreads the live
  // `config` prop (whatever GeneralSection last saved) before overriding
  // only `templates`.
  it("no longer renders the ext-hours buffer control and preserves its value from config on save", () => {
    const onSave = vi.fn();
    render(
      <ThemeProvider>
        <OrderSettingsSection config={{ ...SAMPLE_ORDER_CONFIG, extHoursMarketBufferPct: 2.5 }} onSave={onSave} />
      </ThemeProvider>,
    );
    expect(screen.queryByTestId("ext-buffer")).toBeNull();
    expect(screen.queryByText("Ext-hours market buffer %")).toBeNull();
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].extHoursMarketBufferPct).toBe(2.5);
  });

  // A positive offset must read unambiguously as positive at a glance
  // (negatives already render "-" via String()); 0 stays sign-less.
  it("renders a leading + on a positive offset after nudging up", () => {
    wrap();
    const offset = screen.getByLabelText("offset-buy-5k") as HTMLInputElement;
    expect(offset.value).toBe("0");
    fireEvent.click(screen.getByTestId("offset-buy-5k-up"));
    expect(offset.value).toBe("+0.05");
  });

  it("renders a leading - on a negative offset after nudging down", () => {
    wrap();
    const offset = screen.getByLabelText("offset-buy-5k") as HTMLInputElement;
    fireEvent.click(screen.getByTestId("offset-buy-5k-down"));
    expect(offset.value).toBe("-0.05");
  });

  // Save feedback + auto-close (both props optional — see the two tests below
  // for the omitted-props case).
  it("clicking Save with toast/onClose supplied pushes a success toast and calls onClose", () => {
    const { toast, onClose } = wrapWithToastAndClose();
    fireEvent.click(screen.getByTestId("save"));
    expect(toast.push).toHaveBeenCalledWith({ level: "success", text: "Order templates & hotkeys saved." });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("clicking Save with toast/onClose omitted still calls onSave and does not throw", () => {
    const { onSave } = wrap();
    expect(() => fireEvent.click(screen.getByTestId("save"))).not.toThrow();
    expect(onSave).toHaveBeenCalledTimes(1);
  });
});
