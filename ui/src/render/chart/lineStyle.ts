// Shared line-style vocabulary. Drawings render on the raw 2D canvas (dash arrays);
// indicator series render through LWC (numeric LineStyle enum). Keep both here so the
// three names never drift between the two render paths.
export type LineStyleName = "solid" | "dashed" | "dotted";

export const LINE_STYLE_NAMES: readonly LineStyleName[] = ["solid", "dashed", "dotted"];

// canvas ctx.setLineDash() arrays
export const LINE_DASH: Record<LineStyleName, number[]> = {
  solid: [],
  dashed: [6, 4],
  dotted: [2, 3],
};

// lightweight-charts LineStyle enum: Solid=0, Dotted=1, Dashed=2
export const LWC_LINE_STYLE: Record<LineStyleName, number> = {
  solid: 0,
  dotted: 1,
  dashed: 2,
};
