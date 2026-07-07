// ui/src/render/tape/paintTape.ts
// Pure painter: paint(ctx, state). Newest print on top; y = rowIndex × TAPE_ROW_H.
import { FONTS } from "../palette";
import { TAPE_ROW_H, type TapePaintState } from "./tapeState";

const PAD = 6;

export function paintTape(ctx: CanvasRenderingContext2D, s: TapePaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, s.width, s.height);

  // honesty: a paused tape is visibly not live (the chrome pill is the control;
  // this strip marks the surface itself) — draw unconditionally on `paused` so
  // an empty-rows paused view (e.g. filtered out entirely) still shows it.
  if (s.paused) {
    ctx.fillStyle = p.warn;
    ctx.fillRect(0, 0, s.width, 2);
  }

  if (s.rows.length === 0) {
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillStyle = p.textMuted;
    ctx.font = `11px ${FONTS.mono}`;
    ctx.fillText("no prints yet", s.width / 2, s.height / 2);
    return;
  }

  ctx.textBaseline = "middle";
  for (let i = 0; i < s.rows.length; i++) {
    const top = i * TAPE_ROW_H;
    if (top > s.height) break;
    const r = s.rows[i];
    const midY = top + TAPE_ROW_H / 2;
    const dir = r.direction === "BUY" ? p.up : r.direction === "SELL" ? p.down : p.neutral;

    // full-row tint background — market direction, not app state, hence the
    // up/down/neutral flash tokens rather than a bronze/status color.
    ctx.fillStyle = r.direction === "BUY" ? p.flashBuy : r.direction === "SELL" ? p.flashSell : p.flashNeutral;
    ctx.fillRect(0, top, s.width, TAPE_ROW_H);

    // FONTS.mono for ALL canvas text: the golden harness only registers IBM
    // Plex Mono, so any other family would render with a non-deterministic
    // node-canvas fallback and defeat the pixel goldens.
    ctx.font = `${r.isBlock ? "600 " : ""}11px ${FONTS.mono}`;

    // timestamp dimmed within the row's own direction color (not a separate
    // muted color — it should read as a quieter shade of the same print)
    ctx.globalAlpha = 0.65;
    ctx.fillStyle = dir;
    ctx.textAlign = "left";
    ctx.fillText(r.time, PAD, midY);
    ctx.globalAlpha = 1;

    // price + size at full strength in the direction color
    ctx.fillStyle = dir;
    ctx.textAlign = "right";
    ctx.fillText(r.price, s.width * 0.68, midY);
    ctx.fillText(r.size, s.width - PAD, midY);
  }
}
