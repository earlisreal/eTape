import type { Palette } from "../render/palette";

const kebab = (s: string): string => s.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`);

/** Map each Palette field to a CSS custom property (`borderStrong` → `--border-strong`). */
export function paletteToVars(p: Palette): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(p)) out[`--${kebab(k)}`] = v;
  return out;
}

/** Mirror the palette onto :root so CSS classes can consume `var(--*)`. */
export function applyPaletteVars(root: HTMLElement, p: Palette): void {
  const vars = paletteToVars(p);
  for (const [name, value] of Object.entries(vars)) root.style.setProperty(name, value);
}
