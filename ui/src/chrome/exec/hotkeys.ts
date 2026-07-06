import type { ActionTemplate } from "./actionTemplate";

export interface KeyLike { ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; key: string }
const MODIFIER_KEYS = new Set(["Control", "Shift", "Alt", "Meta"]);

// Canonical combo, modifiers in fixed order Ctrl+Alt+Shift+Meta+Key; single letters
// upper-cased. A bare modifier keypress → "" (never matches a binding).
export function normalizeCombo(e: KeyLike): string {
  if (MODIFIER_KEYS.has(e.key)) return "";
  const parts: string[] = [];
  if (e.ctrlKey) parts.push("Ctrl");
  if (e.altKey) parts.push("Alt");
  if (e.shiftKey) parts.push("Shift");
  if (e.metaKey) parts.push("Meta");
  parts.push(e.key.length === 1 ? e.key.toUpperCase() : e.key);
  return parts.join("+");
}

export function matchTemplate(templates: ActionTemplate[], combo: string): ActionTemplate | undefined {
  if (combo === "") return undefined;
  return templates.find((t) => t.hotkey === combo);
}
