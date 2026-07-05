// Pure painter: paint(ctx, state). Rows are indexed by book level —
// y = rowIndex × LADDER_ROW_H — no viewport/window classes (Plan-1 roadmap).
import { FONTS } from "../palette";
import { formatPrice, formatSize } from "../format";
import { flashAlpha, LADDER_LEVELS, type LadderPaintState, type LadderRow } from "./ladderState";

export const LADDER_ROW_H = 22;
const HEADER_H = 20;
const CENTER_H = 26;
const GUTTER_W = 14; // order-mark gutter on the left edge
const PAD = 6;

const priceRight = (w: number): number => GUTTER_W + (w - GUTTER_W) * 0.32;
const sizeRight = (w: number): number => GUTTER_W + (w - GUTTER_W) * 0.65;

export function paintLadder(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, s.width, s.height);

  if (!s.entitled) {
    drawCenteredNote(ctx, s, "no depth entitlement", `${s.symbol} — full order book is US-only (LV3)`);
    return;
  }
  drawHeader(ctx, s);
  if (s.asks.length === 0 && s.bids.length === 0) {
    drawCenteredNote(ctx, s, "waiting for depth…", s.symbol);
    return;
  }

  const alpha = flashAlpha(s.flash, s.nowMs);
  // Ask block: worst ask at the top, best ask directly above the center row.
  for (let i = 0; i < LADDER_LEVELS; i++) {
    const row = s.asks[LADDER_LEVELS - 1 - i];
    if (row) drawRow(ctx, s, row, HEADER_H + i * LADDER_ROW_H, "ask", alpha);
  }
  drawCenterRow(ctx, s);
  const bidTop = HEADER_H + LADDER_LEVELS * LADDER_ROW_H + CENTER_H;
  for (let i = 0; i < LADDER_LEVELS; i++) {
    const row = s.bids[i];
    if (row) drawRow(ctx, s, row, bidTop + i * LADDER_ROW_H, "bid", alpha);
  }
}

function drawRow(
  ctx: CanvasRenderingContext2D,
  s: LadderPaintState,
  row: LadderRow,
  y: number,
  side: "ask" | "bid",
  alpha: number,
): void {
  const p = s.palette;
  const w = s.width;

  // cumulative depth bar, anchored to the right edge
  const barW = row.cumFraction * (w - GUTTER_W);
  ctx.fillStyle = side === "ask" ? p.depthAsk : p.depthBid;
  ctx.fillRect(w - barW, y, barW, LADDER_ROW_H);

  // last-trade flash behind the matching price row, decayed by age
  if (alpha > 0 && s.flash && s.flash.price === row.price) {
    ctx.globalAlpha = alpha;
    ctx.fillStyle =
      s.flash.direction === "BUY" ? p.flashBuy : s.flash.direction === "SELL" ? p.flashSell : p.flashNeutral;
    ctx.fillRect(GUTTER_W, y, w - GUTTER_W, LADDER_ROW_H);
    ctx.globalAlpha = 1;
  }

  ctx.font = `11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  const midY = y + LADDER_ROW_H / 2;
  ctx.fillStyle = side === "ask" ? p.down : p.up;
  ctx.fillText(formatPrice(row.price, s.decimals), priceRight(w), midY);
  ctx.fillStyle = p.text;
  ctx.fillText(formatSize(row.size), sizeRight(w), midY);
  ctx.fillStyle = p.textMuted;
  ctx.fillText(formatSize(row.cum), w - PAD, midY);

  // display-only working-order marks: gutter triangle + row outline + remaining qty
  const marks = s.orders.filter((o) => o.price === row.price);
  if (marks.length > 0) {
    ctx.strokeStyle = p.orderMark;
    ctx.strokeRect(GUTTER_W + 0.5, y + 0.5, w - GUTTER_W - 1, LADDER_ROW_H - 1);
    ctx.fillStyle = p.orderMark;
    ctx.beginPath();
    ctx.moveTo(3, midY - 4);
    ctx.lineTo(3, midY + 4);
    ctx.lineTo(10, midY);
    ctx.closePath();
    ctx.fill();
    ctx.textAlign = "left";
    ctx.font = `9px ${FONTS.mono}`;
    ctx.fillText(formatSize(marks.reduce((q, m) => q + m.qty, 0)), GUTTER_W + 2, y + 6);
  }
}

function drawHeader(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.surface;
  ctx.fillRect(0, 0, s.width, HEADER_H);
  ctx.strokeStyle = p.border;
  ctx.beginPath();
  ctx.moveTo(0, HEADER_H - 0.5);
  ctx.lineTo(s.width, HEADER_H - 0.5);
  ctx.stroke();
  ctx.font = `10px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  ctx.fillStyle = p.textMuted;
  const midY = HEADER_H / 2;
  ctx.fillText("PRICE", priceRight(s.width), midY);
  ctx.fillText("SIZE", sizeRight(s.width), midY);
  ctx.fillText("CUM", s.width - PAD, midY);
}

function drawCenterRow(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  const y = HEADER_H + LADDER_LEVELS * LADDER_ROW_H;
  ctx.fillStyle = p.surface;
  ctx.fillRect(0, y, s.width, CENTER_H);
  ctx.textBaseline = "middle";
  const midY = y + CENTER_H / 2;
  if (s.last) {
    ctx.font = `bold 13px ${FONTS.mono}`;
    ctx.textAlign = "left";
    ctx.fillStyle = s.last.direction === "BUY" ? p.up : s.last.direction === "SELL" ? p.down : p.neutral;
    ctx.fillText(formatPrice(s.last.price, s.decimals), GUTTER_W + 2, midY);
  }
  if (s.spread !== null) {
    ctx.font = `10px ${FONTS.mono}`;
    ctx.textAlign = "right";
    ctx.fillStyle = p.textMuted;
    ctx.fillText(`Δ ${formatPrice(s.spread, s.decimals)}`, s.width - PAD, midY);
  }
}

function drawCenteredNote(ctx: CanvasRenderingContext2D, s: LadderPaintState, title: string, sub: string): void {
  const p = s.palette;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = p.textMuted;
  ctx.font = `12px ${FONTS.mono}`;
  ctx.fillText(title, s.width / 2, s.height / 2 - 10);
  ctx.font = `10px ${FONTS.mono}`;
  ctx.fillText(sub, s.width / 2, s.height / 2 + 10);
}
