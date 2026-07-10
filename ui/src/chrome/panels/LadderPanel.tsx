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

export function LadderPanel({ config, stores, scheduler, width, height, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette } = useTheme();
  // config.group is frozen (dockview never re-invokes this panel's factory with a
  // fresh config after creation); PanelFrame's live `group` prop is what actually
  // changes on a group re-pick — see registry.ts's PanelProps.group comment.
  const group = groupProp ?? config.group;

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

  // groupRef/reseedForGroupRef bridge a later group re-pick into the mount
  // effect's own closure (which is [config.id]-only — the canvas must never
  // remount on a symbol/group change): the reactive effect below sees live
  // `group` changes and calls back into the mount effect's own re-seed logic.
  const groupRef = useRef(group);
  const reseedForGroupRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    let symbol = linkGroups.symbolFor(groupRef.current) ?? seedSymbol;
    let last: LastTrade | null = null;
    let flash: TradeFlash | null = null;
    let tapeGen = stores.tape.generation(symbol);
    let tapeSeq = 0;

    // Seed "last trade" from this symbol's own ring — O(1) via lastTick(), not
    // the old backward scan over the (formerly global, now per-symbol) ring.
    const seedLast = (): void => {
      const t = stores.tape.lastTick(symbol);
      last = t ? { price: t.price, direction: t.direction } : null;
      tapeSeq = stores.tape.source(symbol).lastSeq();
    };
    seedLast();

    const reseedForGroup = () => {
      const next = linkGroups.symbolFor(groupRef.current) ?? seedSymbol;
      if (next !== symbol) {
        symbol = next;
        flash = null;
        // The new symbol has its own generation counter — refresh tapeGen here,
        // or the very next paint would compare the new symbol's generation
        // against the OLD symbol's stale value and misfire the reconnect branch.
        tapeGen = stores.tape.generation(symbol);
        seedLast();
        forceRef.current++;
      }
    };
    reseedForGroupRef.current = reseedForGroup;
    const offLink = linkGroups.subscribe(reseedForGroup);

    const offExec = stores.exec.subscribe(() => { forceRef.current++; });

    let lastBookRev = -1;
    let lastTapeRev = -1;
    let lastForce = -1;
    const off = scheduler.register({
      id: `ladder:${config.id}`,
      isDirty: () => {
        const bookRev = stores.book.getRev(symbol);
        const tapeRev = stores.tape.getRev(symbol);
        const changed = bookRev !== lastBookRev || tapeRev !== lastTapeRev || forceRef.current !== lastForce;
        lastBookRev = bookRev;
        lastTapeRev = tapeRev;
        lastForce = forceRef.current;
        // an active flash keeps the surface animating until it decays out
        return changed || flashAlpha(flash, performance.now()) > 0;
      },
      paint: () => {
        // reconnect re-sync rebuilt this symbol's ring — reseed instead of walking stale seqs
        if (tapeGen !== stores.tape.generation(symbol)) {
          tapeGen = stores.tape.generation(symbol);
          flash = null;
          seedLast();
        }
        // Per-symbol source: every entry here IS `symbol`, so no more filtering —
        // the walk only visits new ticks for this symbol (was: the whole global ring).
        const src = stores.tape.source(symbol);
        const tip = src.lastSeq();
        for (let q = Math.max(tapeSeq + 1, src.oldestSeq()); q <= tip; q++) {
          const t = src.tickBySeq(q);
          if (t) {
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
          orders: stores.exec.workingOrdersFor(symbol),
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

  // This panel's own group was reassigned (as opposed to the group's focused
  // symbol changing, which linkGroups.subscribe above already handles). Guard
  // is a no-op on mount (groupRef seeds to the same initial `group`).
  useEffect(() => {
    if (groupRef.current !== group) {
      groupRef.current = group;
      reseedForGroupRef.current?.();
    }
  }, [group]);

  return <canvas ref={canvasRef} style={{ display: "block", width: "100%", height: "100%" }} />;
}
