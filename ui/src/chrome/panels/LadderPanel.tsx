// L2 ladder panel: canvas mounted once, painted imperatively by the scheduler.
// High-frequency data (book, ticks) never touches React state; the only React
// here is the mount itself. Store dirtiness is observed via getRev() cursors
// (BookStore/TapeRing are shared across panels — never consumeDirty()).
import { useEffect, useRef } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { applyCanvasSize } from "../../render/canvas";
import { buildLadderState, flashAlpha, type LastTrade, type TradeFlash } from "../../render/ladder/ladderState";
import { paintLadder } from "../../render/ladder/paintLadder";

export function LadderPanel({ config, stores, scheduler, width, height, linkGroups }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette } = useTheme();

  // Refs bridge React-world changes (size, theme) into the paint loop without
  // remounting the surface; forceRef bumps mark the surface dirty.
  const paletteRef = useRef(palette);
  paletteRef.current = palette;
  const sizeRef = useRef({ width, height });
  sizeRef.current = { width, height };
  const forceRef = useRef(0);
  useEffect(() => {
    forceRef.current++;
  }, [width, height, palette]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    let symbol = linkGroups.symbolFor(config.group) ?? seedSymbol;
    let last: LastTrade | null = null;
    let flash: TradeFlash | null = null;
    let tapeGen = stores.tape.generation();
    let tapeSeq = 0;

    // Seed "last trade" from whatever the shared ring already holds for us.
    const seedLast = (): void => {
      last = null;
      for (let q = stores.tape.lastSeq(); q >= stores.tape.oldestSeq(); q--) {
        const t = stores.tape.tickBySeq(q);
        if (t && t.symbol === symbol) {
          last = { price: t.price, direction: t.direction };
          break;
        }
      }
      tapeSeq = stores.tape.lastSeq();
    };
    seedLast();

    const offLink = linkGroups.subscribe(() => {
      const next = linkGroups.symbolFor(config.group) ?? seedSymbol;
      if (next !== symbol) {
        symbol = next;
        flash = null;
        seedLast();
        forceRef.current++;
      }
    });

    let orders: unknown[] = stores.exec.getSnapshot().orders;
    const offExec = stores.exec.subscribe(() => {
      orders = stores.exec.getSnapshot().orders;
      forceRef.current++;
    });

    let lastBookRev = -1;
    let lastTapeRev = -1;
    let lastForce = -1;
    const off = scheduler.register({
      id: `ladder:${config.id}`,
      isDirty: () => {
        const bookRev = stores.book.getRev();
        const tapeRev = stores.tape.getRev();
        const changed = bookRev !== lastBookRev || tapeRev !== lastTapeRev || forceRef.current !== lastForce;
        lastBookRev = bookRev;
        lastTapeRev = tapeRev;
        lastForce = forceRef.current;
        // an active flash keeps the surface animating until it decays out
        return changed || flashAlpha(flash, performance.now()) > 0;
      },
      paint: () => {
        // reconnect re-sync rebuilt the ring — reseed instead of walking stale seqs
        if (tapeGen !== stores.tape.generation()) {
          tapeGen = stores.tape.generation();
          flash = null;
          seedLast();
        }
        const tip = stores.tape.lastSeq();
        for (let q = Math.max(tapeSeq + 1, stores.tape.oldestSeq()); q <= tip; q++) {
          const t = stores.tape.tickBySeq(q);
          if (t && t.symbol === symbol) {
            last = { price: t.price, direction: t.direction };
            flash = { price: t.price, direction: t.direction, atMs: performance.now() };
          }
        }
        tapeSeq = tip;

        const { width: w, height: h } = sizeRef.current;
        if (!applyCanvasSize(canvas, ctx, w, h, window.devicePixelRatio || 1)) return;
        paintLadder(ctx, buildLadderState({
          symbol,
          book: stores.book.get(symbol),
          orders,
          flash,
          last,
          nowMs: performance.now(),
          width: w,
          height: h,
          palette: paletteRef.current,
        }));
      },
    });

    return () => {
      off();
      offLink();
      offExec();
    };
  }, [config.id]);

  return <canvas ref={canvasRef} style={{ display: "block", width: "100%", height: "100%" }} />;
}
