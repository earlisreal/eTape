// Pure painter: paint(ctx, state). Classic two-column DOM ladder — bids left
// (green prices), asks right (red prices), a center divider, depth bars
// growing outward from the divider. Logical rows share one scroll offset;
// beyond-depth LULD fallbacks occupy each side's bottom visible slot.
import { FONTS } from "../palette";
import { formatPrice, formatSize, quoteDecimals } from "../format";
import {
  flashAlpha,
  LADDER_HEADER_H,
  LADDER_ROW_H,
  LADDER_SPREAD_H,
  type LadderPaintState,
  type LadderEntry,
  type LULDBoundaryRow,
  isLULDBoundaryRow,
  visibleLadderRows,
} from "./ladderState";
export { LADDER_ROW_H } from "./ladderState";

const PAD = 8;
const ORDER_EDGE = 3; // bronze inner-edge width for a working-order mark

export function paintLadder(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  const w = s.width;
  ctx.clearRect(0, 0, w, s.height);
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, w, s.height);

  if (!s.entitled) {
    drawCentered(ctx, s, "L2 depth not entitled", `${s.symbol} — full order book is US-only (LV3)`);
    return;
  }
  if (s.asks.length === 0 && s.bids.length === 0) {
    drawCentered(ctx, s, "waiting for depth…", s.symbol);
    return;
  }

  const mid = Math.round(w / 2);

  // spread line
  ctx.font = `10px ${FONTS.mono}`;
  ctx.fillStyle = p.textMuted;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  const strip = spreadLabel(s);
  if (strip) ctx.fillText(strip, mid, LADDER_SPREAD_H / 2);
  ctx.strokeStyle = p.border;
  ctx.beginPath();
  ctx.moveTo(0, LADDER_SPREAD_H - 0.5);
  ctx.lineTo(w, LADDER_SPREAD_H - 0.5);
  ctx.stroke();

  // column header: SIZE | BID ‖ ASK | SIZE
  const headY = LADDER_SPREAD_H + LADDER_HEADER_H / 2;
  // FONTS.mono for ALL canvas text: the golden harness only registers IBM Plex
  // Mono, so any FONTS.sans text would render with a non-deterministic
  // node-canvas fallback and defeat the pixel goldens.
  ctx.font = `9.5px ${FONTS.mono}`;
  ctx.fillStyle = p.textMuted;
  ctx.textAlign = "left";
  ctx.fillText("SIZE", PAD, headY);
  ctx.textAlign = "right";
  ctx.fillText("BID", mid - PAD, headY);
  ctx.textAlign = "left";
  ctx.fillText("ASK", mid + PAD, headY);
  ctx.textAlign = "right";
  ctx.fillText("SIZE", w - PAD, headY);

  // center divider — borderStrong per the palette's own field comment
  ctx.strokeStyle = p.borderStrong;
  ctx.beginPath();
  ctx.moveTo(mid + 0.5, LADDER_SPREAD_H);
  ctx.lineTo(mid + 0.5, s.height);
  ctx.stroke();

  const top = LADDER_SPREAD_H + LADDER_HEADER_H;
  const visibleRows = visibleLadderRows(s.height);
  drawRows(ctx, s, s.bids, s.bidFallback, "bid", mid, top, visibleRows);
  drawRows(ctx, s, s.asks, s.askFallback, "ask", mid, top, visibleRows);
}

function drawRows(
  ctx: CanvasRenderingContext2D,
  s: LadderPaintState,
  rows: LadderEntry[],
  fallback: LULDBoundaryRow | null,
  side: "bid" | "ask",
  mid: number,
  top: number,
  visibleRows: number,
): void {
  if (visibleRows <= 0) return;
  const logicalRows = Math.max(0, visibleRows - (fallback ? 1 : 0));
  for (let i = s.rowOffset; i < rows.length && i - s.rowOffset < logicalRows; i++) {
    drawSide(ctx, s, rows[i], side, mid, top + (i - s.rowOffset) * LADDER_ROW_H);
  }
  if (fallback) drawSide(ctx, s, fallback, side, mid, top + (visibleRows - 1) * LADDER_ROW_H);
}

function drawSide(
  ctx: CanvasRenderingContext2D,
  s: LadderPaintState,
  row: LadderEntry,
  side: "bid" | "ask",
  mid: number,
  y: number,
): void {
  if (isLULDBoundaryRow(row)) {
    drawLULDBoundary(ctx, s, row, side, mid, y);
    return;
  }
  const p = s.palette;
  const w = s.width;

  // depth bar, grows outward from the divider
  const half = side === "bid" ? mid : w - mid;
  const barLen = row.sizeFraction * half;
  ctx.fillStyle = side === "bid" ? p.depthBid : p.depthAsk;
  if (side === "bid") ctx.fillRect(mid - barLen, y, barLen, LADDER_ROW_H);
  else ctx.fillRect(mid, y, barLen, LADDER_ROW_H);

  // last-trade flash over this row's half, decayed by age
  const alpha = flashAlpha(s.flash, s.nowMs);
  if (alpha > 0 && s.flash && s.flash.price === row.price) {
    ctx.globalAlpha = alpha;
    ctx.fillStyle =
      s.flash.direction === "BUY" ? p.flashBuy : s.flash.direction === "SELL" ? p.flashSell : p.flashNeutral;
    if (side === "bid") ctx.fillRect(0, y, mid, LADDER_ROW_H);
    else ctx.fillRect(mid, y, w - mid, LADDER_ROW_H);
    ctx.globalAlpha = 1;
  }

  // price near the divider (market-direction color), size at the outer edge
  const isAverageEntry = s.averageEntryPrice !== null && row.price === s.averageEntryPrice;
  ctx.font = `${isAverageEntry ? "700 " : ""}11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  const midY = y + LADDER_ROW_H / 2;
  if (side === "bid") {
    ctx.textAlign = "left";
    ctx.fillStyle = p.text;
    ctx.fillText(formatSize(row.size), PAD, midY);
    ctx.textAlign = "right";
    ctx.fillStyle = p.up;
    ctx.fillText(formatPrice(row.price, quoteDecimals(row.price)), mid - PAD, midY);
  } else {
    ctx.textAlign = "left";
    ctx.fillStyle = p.down;
    ctx.fillText(formatPrice(row.price, quoteDecimals(row.price)), mid + PAD, midY);
    ctx.textAlign = "right";
    ctx.fillStyle = p.text;
    ctx.fillText(formatSize(row.size), w - PAD, midY);
  }

  if (isAverageEntry) {
    ctx.strokeStyle = p.text;
    ctx.lineWidth = 1;
    const borderX = side === "bid" ? 0.5 : mid + 1.5;
    const borderWidth = side === "bid" ? mid - 1 : w - mid - 2;
    if (borderWidth > 0) ctx.strokeRect(borderX, y + 0.5, borderWidth, LADDER_ROW_H - 1);
  }

  // display-only working-order mark: bronze inner edge on the divider side
  const hasOrder = s.orders.some((o) => o.price === row.price);
  if (hasOrder) {
    ctx.fillStyle = p.orderMark;
    if (side === "bid") ctx.fillRect(mid - ORDER_EDGE, y, ORDER_EDGE, LADDER_ROW_H);
    else ctx.fillRect(mid, y, ORDER_EDGE, LADDER_ROW_H);
  }
}

function drawLULDBoundary(
  ctx: CanvasRenderingContext2D,
  s: LadderPaintState,
  row: LULDBoundaryRow,
  side: "bid" | "ask",
  mid: number,
  y: number,
): void {
  const label = row.frozen ? "LULD · FROZEN" : "LULD";
  const midY = y + LADDER_ROW_H / 2;
  ctx.font = `11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  ctx.fillStyle = s.palette.warn;
  if (side === "bid") {
    ctx.textAlign = "left";
    ctx.fillText(label, PAD, midY);
    ctx.textAlign = "right";
    ctx.fillText(formatPrice(row.price, quoteDecimals(row.price)), mid - PAD, midY);
  } else {
    ctx.textAlign = "left";
    ctx.fillText(formatPrice(row.price, quoteDecimals(row.price)), mid + PAD, midY);
    ctx.textAlign = "right";
    ctx.fillText(label, s.width - PAD, midY);
  }
}

/** "3.49 × 3.51 · spread 0.02" — best bid × best ask with the spread called out. */
function spreadLabel(s: LadderPaintState): string {
  const bid = s.bids.find((row) => !isLULDBoundaryRow(row));
  const ask = s.asks.find((row) => !isLULDBoundaryRow(row));
  if (!bid || !ask || s.spread === null) return "";
  return `${formatPrice(bid.price, quoteDecimals(bid.price))} × ${formatPrice(ask.price, quoteDecimals(ask.price))} · spread ${formatPrice(s.spread, quoteDecimals(Math.abs(s.spread)))}`;
}

function drawCentered(ctx: CanvasRenderingContext2D, s: LadderPaintState, title: string, sub?: string): void {
  const p = s.palette;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = p.textMuted;
  ctx.font = `12px ${FONTS.mono}`;
  ctx.fillText(title, s.width / 2, s.height / 2 - (sub ? 10 : 0));
  if (sub) {
    ctx.font = `10px ${FONTS.mono}`;
    ctx.fillText(sub, s.width / 2, s.height / 2 + 10);
  }
}
