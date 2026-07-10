import { describe, it, expect } from "vitest";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import type { ActionTemplate } from "./actionTemplate";

// Local fixture — DEFAULT_TEMPLATES ships empty (eTape has no default
// hotkeys), so matchTemplate's own matching behavior is exercised here
// against a stand-in template set instead.
const SAMPLE_TEMPLATES: ActionTemplate[] = [
  { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
  { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
];

describe("hotkey matcher", () => {
  it("normalizes modifiers into a canonical combo string", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "1" })).toBe("Ctrl+1");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, key: "k" })).toBe("Ctrl+Shift+K");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Backspace" })).toBe("Ctrl+Backspace");
  });
  it("returns empty for a bare modifier keypress", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Control" })).toBe("");
  });
  // Regression: Shift+1's e.key is the shifted glyph "!" (browsers report the
  // character actually produced, not the digit), so relying on e.key alone
  // mislabels the combo as "Shift+!". e.code is layout/shift-independent
  // ("Digit1"), so prefer it when present.
  it("normalizes a Shift+digit combo from e.code, not the shifted glyph in e.key", () => {
    expect(normalizeCombo({ ctrlKey: false, shiftKey: true, altKey: false, metaKey: false, key: "!", code: "Digit1" })).toBe("Shift+1");
  });
  it("normalizes a letter combo from e.code the same as from e.key", () => {
    expect(normalizeCombo({ ctrlKey: false, shiftKey: true, altKey: false, metaKey: false, key: "K", code: "KeyK" })).toBe("Shift+K");
  });
  it("normalizes a numpad digit combo from e.code", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "1", code: "Numpad1" })).toBe("Ctrl+1");
  });
  it("falls back to e.key when e.code is absent", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "5" })).toBe("Ctrl+5");
  });
  it("matches a template by its hotkey field", () => {
    expect(matchTemplate(SAMPLE_TEMPLATES, "Ctrl+1")?.id).toBe("buy-5k");
    expect(matchTemplate(SAMPLE_TEMPLATES, "Ctrl+Shift+K")?.id).toBe("kill");
    expect(matchTemplate(SAMPLE_TEMPLATES, "Ctrl+9")).toBeUndefined();
  });
});
