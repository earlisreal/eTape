import type { Palette } from "../render/palette";

// MenuChrome is the 5-field structural subset TVContextMenu actually reads
// (verified: surface, border, text, hover, down). TvChrome satisfies it
// structurally, so chart callers pass their existing chrome unchanged.
export interface MenuChrome {
  surface: string;
  border: string;
  text: string;
  hover: string;
  down: string; // danger-entry text color
}

// menuChrome adapts the app Palette (which has no `hover` token) for non-chart
// context-menu callers. hover is synthesized from borderStrong; danger text
// maps to palette.danger.
export function menuChrome(palette: Palette): MenuChrome {
  return {
    surface: palette.surface,
    border: palette.border,
    text: palette.text,
    hover: palette.borderStrong,
    down: palette.danger,
  };
}
