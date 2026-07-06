import { describe, it, expect } from "vitest";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { DEFAULT_TEMPLATES } from "./actionTemplate";

describe("hotkey matcher", () => {
  it("normalizes modifiers into a canonical combo string", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "1" })).toBe("Ctrl+1");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, key: "k" })).toBe("Ctrl+Shift+K");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Backspace" })).toBe("Ctrl+Backspace");
  });
  it("returns empty for a bare modifier keypress", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Control" })).toBe("");
  });
  it("matches a template by its hotkey field", () => {
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+1")?.id).toBe("buy-5k");
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+Shift+K")?.id).toBe("kill");
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+9")).toBeUndefined();
  });
});
