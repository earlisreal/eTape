import type { ActionTemplate } from "./actionTemplate";

export interface KeyLike { ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; key: string; code?: string }
const MODIFIER_KEYS = new Set(["Control", "Shift", "Alt", "Meta"]);

// Physical-key label for the base (non-modifier) key, preferring e.code so a
// Shift+digit reads as the digit rather than the shifted glyph (Shift+1's
// e.key is "!", not "1" — e.code is the layout-independent "Digit1"). Falls
// back to e.key when code is absent (e.g. tests that don't set it), so
// existing unshifted-key call sites are unaffected.
function baseLabel(e: KeyLike): string {
  const code = e.code ?? "";
  if (/^Digit[0-9]$/.test(code)) return code.slice(5);   // Digit1 -> "1"
  if (/^Numpad[0-9]$/.test(code)) return code.slice(6);  // Numpad1 -> "1"
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);     // KeyK -> "K"
  return e.key.length === 1 ? e.key.toUpperCase() : e.key;
}

// Canonical combo, modifiers in fixed order Ctrl+Alt+Shift+Meta+Key; single letters
// upper-cased. A bare modifier keypress → "" (never matches a binding).
export function normalizeCombo(e: KeyLike): string {
  if (MODIFIER_KEYS.has(e.key)) return "";
  const parts: string[] = [];
  if (e.ctrlKey) parts.push("Ctrl");
  if (e.altKey) parts.push("Alt");
  if (e.shiftKey) parts.push("Shift");
  if (e.metaKey) parts.push("Meta");
  parts.push(baseLabel(e));
  return parts.join("+");
}

export function matchTemplate(templates: ActionTemplate[], combo: string): ActionTemplate | undefined {
  if (combo === "") return undefined;
  return templates.find((t) => t.hotkey === combo);
}
