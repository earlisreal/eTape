// Time & sales panel: canvas tape over the shared TapeRing. The min-size input
// and paused pill are low-rate user-event React state (allowed); tick flow
// itself never touches React. Wheel scrolling pauses; the anchor is a
// (seq, generation) pair so a reconnect re-sync honestly drops the pause —
// and so does an anchor that simply ages out of the retained ring while
// paused (Task 7's buildTapeRows eviction fix).
import { useEffect, useRef, useState } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { applyCanvasSize } from "../../render/canvas";
import { scrollAccumulate } from "../../render/scroll";
import { FONTS } from "../../render/palette";
import {
  adjustAnchor, buildTapeRows, liveView, TAPE_ROW_H, type TapeView,
} from "../../render/tape/tapeState";
import { paintTape, TAPE_PAD, TAPE_PRICE_RIGHT_FRAC } from "../../render/tape/paintTape";

const HEADER_H = 26;
const COLHEAD_H = 16;

export function TapePanel({ config, stores, scheduler, width, height, linkGroups, onConfigChange }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette } = useTheme();
  const [paused, setPaused] = useState(false);
  const [minSize, setMinSize] = useState<number>(
    typeof config.settings.minSize === "number" ? config.settings.minSize : 0,
  );

  const paletteRef = useRef(palette);
  paletteRef.current = palette;
  const sizeRef = useRef({ width, height });
  sizeRef.current = { width, height };
  const minSizeRef = useRef(minSize);
  minSizeRef.current = minSize;
  const viewRef = useRef<TapeView>({ anchorSeq: null, generation: 0 });
  const remainderRef = useRef(0);
  const symbolRef = useRef("");
  const forceRef = useRef(0);
  useEffect(() => {
    forceRef.current++;
  }, [width, height, palette, minSize]);

  const jumpToLive = (): void => {
    viewRef.current = liveView(stores.tape);
    remainderRef.current = 0;
    setPaused(false);
    forceRef.current++;
  };

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    symbolRef.current = linkGroups.symbolFor(config.group) ?? seedSymbol;
    const offLink = linkGroups.subscribe(() => {
      const next = linkGroups.symbolFor(config.group) ?? seedSymbol;
      if (next !== symbolRef.current) {
        symbolRef.current = next;
        jumpToLive();
      }
    });

    // Native non-passive listener: React attaches wheel passively at the root,
    // which would ignore preventDefault and let the page scroll.
    const onWheel = (e: WheelEvent): void => {
      e.preventDefault();
      const acc = scrollAccumulate(remainderRef.current, e.deltaY, TAPE_ROW_H);
      remainderRef.current = acc.remainder;
      if (acc.rows === 0) return;
      // wheel up (deltaY < 0) → negative rows → older; wheel down → toward live
      viewRef.current = adjustAnchor(stores.tape, viewRef.current, acc.rows, {
        symbol: symbolRef.current,
        minSize: minSizeRef.current,
      });
      setPaused(viewRef.current.anchorSeq !== null); // low-rate: one per wheel event
      forceRef.current++;
    };
    canvas.addEventListener("wheel", onWheel, { passive: false });

    let lastTapeRev = -1;
    let lastForce = -1;
    const off = scheduler.register({
      id: `tape:${config.id}`,
      isDirty: () => {
        const rev = stores.tape.getRev();
        const changed = rev !== lastTapeRev || forceRef.current !== lastForce;
        lastTapeRev = rev;
        lastForce = forceRef.current;
        return changed;
      },
      paint: () => {
        // reconnect rebuilt the ring while paused → the anchor is meaningless;
        // resume live and drop the pill (once per reconnect — low-rate setState)
        if (viewRef.current.anchorSeq !== null && viewRef.current.generation !== stores.tape.generation()) {
          viewRef.current = liveView(stores.tape);
          setPaused(false);
        }
        const { width: w, height: h } = sizeRef.current;
        const canvasH = h - HEADER_H - COLHEAD_H;
        if (!applyCanvasSize(canvas, ctx, w, canvasH, window.devicePixelRatio || 1)) return;
        const { rows, paused: p } = buildTapeRows(stores.tape, viewRef.current, {
          symbol: symbolRef.current,
          minSize: minSizeRef.current,
          maxRows: Math.ceil(canvasH / TAPE_ROW_H) + 1,
        });
        // The anchor can also age out of the retained ring without any reconnect
        // (a long pause + enough delta volume to overrun the ring's capacity) —
        // buildTapeRows already falls back to a live-equivalent window in that
        // case (Task 7's eviction fix), reporting paused: false even though
        // viewRef still holds a (now-meaningless) anchorSeq. Gate on the ref,
        // not the React `paused` state (this closure only sees `paused` as of
        // mount) — mutating viewRef here also makes the check self-clearing so
        // this only fires once per eviction, not every subsequent paint tick.
        if (!p && viewRef.current.anchorSeq !== null) {
          viewRef.current = liveView(stores.tape);
          setPaused(false);
        }
        paintTape(ctx, { rows, paused: p, width: w, height: canvasH, palette: paletteRef.current });
      },
    });

    return () => {
      off();
      offLink();
      canvas.removeEventListener("wheel", onWheel);
    };
  }, [config.id]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          display: "flex", alignItems: "center", gap: 8, height: HEADER_H, padding: "0 6px",
          background: palette.surface, borderBottom: `1px solid ${palette.border}`,
          fontSize: 11, fontFamily: FONTS.sans, color: palette.textMuted, flex: "none",
        }}
      >
        <label>
          min size{" "}
          <input
            type="number" min={0} value={minSize} style={{ width: 64 }}
            onChange={(e) => {
              const v = Math.max(0, Number(e.target.value) || 0);
              setMinSize(v);
              onConfigChange({ ...config.settings, minSize: v });
            }}
          />
        </label>
        {paused && (
          <button
            onClick={jumpToLive}
            style={{ marginLeft: "auto", color: palette.warn, background: "none", border: "none", cursor: "pointer", fontSize: 11 }}
          >
            ⏸ paused — jump to live
          </button>
        )}
      </div>
      <div
        style={{
          position: "relative", height: COLHEAD_H, flex: "none",
          background: palette.surface, borderBottom: `1px solid ${palette.border}`,
          fontSize: 10, fontFamily: FONTS.mono, color: palette.textMuted, textTransform: "uppercase",
        }}
      >
        <span style={{ position: "absolute", left: TAPE_PAD, top: "50%", transform: "translateY(-50%)" }}>Time</span>
        <span style={{ position: "absolute", right: `${(1 - TAPE_PRICE_RIGHT_FRAC) * 100}%`, top: "50%", transform: "translateY(-50%)" }}>Price</span>
        <span style={{ position: "absolute", right: TAPE_PAD, top: "50%", transform: "translateY(-50%)" }}>Size</span>
      </div>
      <canvas ref={canvasRef} style={{ display: "block", flex: 1, minHeight: 0 }} />
    </div>
  );
}
