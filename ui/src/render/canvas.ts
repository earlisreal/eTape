/**
 * Size a canvas's backing store for the device pixel ratio and normalize the
 * context transform so painters draw in CSS pixels. Assigning width/height
 * clears the canvas, so only touch them when they actually change. Returns
 * false when the CSS size is not yet usable (panel still measuring).
 */
export function applyCanvasSize(
  canvas: HTMLCanvasElement,
  ctx: CanvasRenderingContext2D,
  cssWidth: number,
  cssHeight: number,
  dpr: number,
): boolean {
  if (cssWidth <= 0 || cssHeight <= 0) return false;
  const w = Math.round(cssWidth * dpr);
  const h = Math.round(cssHeight * dpr);
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  return true;
}
