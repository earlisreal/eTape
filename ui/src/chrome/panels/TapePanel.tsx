// Time & sales panel: canvas tape over the shared TapeRing. The min-size setting
// and paused pill are low-rate user-event React state (allowed); tick flow
// itself never touches React. Wheel scrolling pauses; the anchor is a
// (seq, generation) pair so a reconnect re-sync honestly drops the pause —
// and so does an anchor that simply ages out of the retained ring while
// paused (Task 7's buildTapeRows eviction fix). The min-size input lives in a
// settings dialog reached via a header gear (portaled into PanelFrame's ledger
// header, beside the close button) rather than an inline control in the body.
import { useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { HoverButton } from "../controls/HoverButton";
import { applyCanvasSize } from "../../render/canvas";
import { scrollAccumulate } from "../../render/scroll";
import { FONTS } from "../../render/palette";
import { getTvChrome } from "../../render/chart/tvTheme";
import { PanelHeaderActionsSlotContext } from "./headerSlot";
import { IconGear } from "./tv/tvIcons";
import { TapeSettingsDialog } from "./TapeSettingsDialog";
import {
  adjustAnchor, buildTapeRows, liveView, TAPE_ROW_H, type TapeView,
} from "../../render/tape/tapeState";
import { paintTape, TAPE_PAD, TAPE_PRICE_RIGHT_FRAC } from "../../render/tape/paintTape";

const HEADER_H = 26;
const COLHEAD_H = 16;

export function TapePanel({ config, stores, scheduler, width, height, linkGroups, onConfigChange, group: groupProp }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette, mode } = useTheme();
  const [paused, setPaused] = useState(false);
  const [minSize, setMinSize] = useState<number>(
    typeof config.settings.minSize === "number" ? config.settings.minSize : 0,
  );
  const [settingsOpen, setSettingsOpen] = useState(false);
  // undefined: no PanelFrame above (body-level test) — render the gear inline.
  // null: PanelFrame is present but its actions-slot div hasn't mounted yet.
  // HTMLElement: the live portal target beside the close button.
  const actionsSlot = useContext(PanelHeaderActionsSlotContext);
  // config.group is frozen (dockview never re-invokes this panel's factory with a
  // fresh config after creation); PanelFrame's live `group` prop is what actually
  // changes on a group re-pick — see registry.ts's PanelProps.group comment.
  const group = groupProp ?? config.group;

  const paletteRef = useRef(palette);
  paletteRef.current = palette;
  const sizeRef = useRef({ width, height });
  sizeRef.current = { width, height };
  const minSizeRef = useRef(minSize);
  minSizeRef.current = minSize;
  // The header strip (this ref's consumer, in the paint callback below) only
  // renders — and only takes up canvas height — while paused.
  const pausedRef = useRef(paused);
  pausedRef.current = paused;
  const viewRef = useRef<TapeView>({ anchorSeq: null, generation: 0 });
  const remainderRef = useRef(0);
  const symbolRef = useRef("");
  const forceRef = useRef(0);
  const groupRef = useRef(group);
  useEffect(() => {
    forceRef.current++;
  }, [width, height, palette, minSize, paused]);

  const jumpToLive = (): void => {
    viewRef.current = liveView(stores.tape);
    remainderRef.current = 0;
    setPaused(false);
    forceRef.current++;
  };

  // This panel's own group was reassigned (as opposed to the group's focused
  // symbol changing, which linkGroups.subscribe below already handles). Guard
  // is a no-op on mount (groupRef seeds to the same initial `group`).
  useEffect(() => {
    if (groupRef.current !== group) {
      groupRef.current = group;
      const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
      const next = linkGroups.symbolFor(groupRef.current) ?? seedSymbol;
      if (next !== symbolRef.current) {
        symbolRef.current = next;
        jumpToLive();
      }
    }
  }, [group]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    symbolRef.current = linkGroups.symbolFor(groupRef.current) ?? seedSymbol;
    const offLink = linkGroups.subscribe(() => {
      const next = linkGroups.symbolFor(groupRef.current) ?? seedSymbol;
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
          // Sync the ref immediately: canvasH (right below) reads pausedRef, and
          // setPaused's re-render (which would otherwise update it) hasn't
          // happened yet — without this, this same paint call sizes the canvas
          // for a header strip that's already gone, a one-frame height snap.
          pausedRef.current = false;
        }
        const { width: w, height: h } = sizeRef.current;
        // Header strip only renders while paused (see the JSX below) — give its
        // 26px back to the canvas the rest of the time.
        const canvasH = h - (pausedRef.current ? HEADER_H : 0) - COLHEAD_H;
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
          pausedRef.current = false; // same reasoning as the reconnect branch above
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

  // Portaled into PanelFrame's ledger-header actions slot, beside the close
  // button (see headerSlot.ts's PanelHeaderActionsSlotContext). undefined (no
  // frame above, e.g. a body-level test) falls back to rendering inline; null
  // (frame present, slot div not yet mounted) renders nothing for that tick.
  const gearBtn = (
    <button type="button" aria-label="tape settings" onClick={() => setSettingsOpen(true)}
      title={minSize > 0 ? `min size ${minSize}` : undefined}
      style={{ position: "relative", display: "inline-flex", border: "none", background: "transparent",
        color: palette.textMuted, cursor: "pointer", padding: 3 }}>
      <IconGear size={13} />
      {/* The old inline input always showed its value; the gear alone doesn't —
          this dot is the at-a-glance "a filter is active" cue the title
          attribute (hover-only) can't provide. */}
      {minSize > 0 && (
        <span aria-hidden="true" data-testid="tape-minsize-active"
          style={{ position: "absolute", top: 1, right: 1, width: 5, height: 5, borderRadius: "50%", background: palette.accent }} />
      )}
    </button>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {actionsSlot === undefined ? gearBtn : actionsSlot ? createPortal(gearBtn, actionsSlot) : null}
      {paused && (
        <div
          style={{
            display: "flex", alignItems: "center", justifyContent: "flex-end", height: HEADER_H, padding: "0 6px",
            background: palette.surface, borderBottom: `1px solid ${palette.border}`,
            fontSize: 11, fontFamily: FONTS.sans, color: palette.textMuted, flex: "none",
          }}
        >
          <HoverButton
            onClick={jumpToLive}
            style={{ color: palette.warn, background: "none", border: "none", cursor: "pointer", fontSize: 11 }}
          >
            ⏸ paused — jump to live
          </HoverButton>
        </div>
      )}
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
      {settingsOpen && (
        <TapeSettingsDialog chrome={getTvChrome(mode)} minSize={minSize}
          onClose={() => setSettingsOpen(false)}
          onApply={(v) => { setMinSize(v); onConfigChange({ minSize: v }); }} />
      )}
    </div>
  );
}
