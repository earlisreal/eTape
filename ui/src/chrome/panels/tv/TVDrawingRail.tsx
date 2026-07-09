// ui/src/chrome/panels/tv/TVDrawingRail.tsx
//
// Horizontal, draggable drawing toolbar floating over the chart. It defaults to
// the top-center of the host (clear of the legend text at the top-left) and can
// be dragged anywhere inside the host via the grip handle; the position is
// reported on drag end so the panel can persist it with the workspace.
//
// There is deliberately no "cursor" tool button: committing a drawing already
// reverts to select (TradingView behavior, interaction.ts), Esc cancels an armed
// tool, and re-clicking the armed tool's button toggles back to select.
import { useEffect, useRef, useState } from "react";
import type { CSSProperties, PointerEvent as ReactPointerEvent } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import type { Tool } from "../../../render/chart/drawings/interaction";
import { IconGrip, IconTrend, IconHLine, IconExtended, IconRect, IconMeasure, IconEye, IconEyeOff, IconTrash } from "./tvIcons";
import { HoverButton } from "../../controls/HoverButton";

export interface RailPos { x: number; y: number }

export interface TVDrawingRailProps {
  chrome: TvChrome; activeTool: Tool; hideAll: boolean; symbol: string;
  onSelectTool: (t: Tool) => void; onToggleHideAll: () => void;
  hasSelection: () => boolean; onDeleteSelection: () => void; onClearAll: () => void;
  initialPos?: RailPos | null; onPosChange?: (p: RailPos) => void;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), Math.max(lo, hi));

export function TVDrawingRail({ chrome, activeTool, hideAll, symbol, onSelectTool, onToggleHideAll, hasSelection, onDeleteSelection, onClearAll, initialPos, onPosChange }: TVDrawingRailProps): JSX.Element {
  const [confirm, setConfirm] = useState(false);
  const [pos, setPos] = useState<RailPos | null>(initialPos ?? null);
  const railRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ dx: number; dy: number } | null>(null);
  const posRef = useRef<RailPos | null>(pos);
  const bare = symbol.replace(/^US\./, "");

  const btn = (active: boolean): CSSProperties => ({ width: TV_GEOM.iconBtn, height: TV_GEOM.iconBtn, display: "grid",
    placeItems: "center", background: active ? chrome.hover : "transparent", border: "none", borderRadius: TV_GEOM.radius,
    color: active ? chrome.accent : chrome.text, cursor: "pointer",
    boxShadow: active ? `inset 0 0 0 1px ${chrome.accent}` : "none" });
  // Active tool hovers to a no-visual-op relative to its own active look (ring +
  // accent persist via the base `style` above); inactive tools get the plain
  // grey/text overlay. Never leave hoverStyle undefined — HoverButton's default
  // overlay is the *app* palette, which is wrong for the TV island.
  const toolHover = (active: boolean): CSSProperties => ({ background: chrome.hover, color: active ? chrome.accent : chrome.text });
  // Popovers hang below the horizontal bar (they used to fly out to the right of
  // the old vertical rail).
  const popover: CSSProperties = { position: "absolute", left: 0, top: TV_GEOM.iconBtn + 6, zIndex: 20, background: chrome.surface,
    border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)", padding: 4 };

  // Re-click of the armed tool disarms back to select — the only affordance for
  // it now that the explicit cursor button is gone.
  const toggleTool = (t: Tool, active: boolean) => onSelectTool(active ? "select" : t);
  const onTrash = () => { if (hasSelection()) onDeleteSelection(); else setConfirm(true); };

  // Grip drag: window-level move/up listeners for the drag's duration (NOT
  // setPointerCapture — capture throws NotFoundError for synthetic/automation
  // pointers with no "active pointer", and window listeners also keep the drag
  // alive when the cursor outruns the 14px grip). Position is clamped to the
  // host so the bar can't be lost off-canvas; only drag end reports upward
  // (persisting every mousemove would spam workspace saves).
  const endDragRef = useRef<(() => void) | null>(null);
  useEffect(() => () => endDragRef.current?.(), []);
  const onGripDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    const rail = railRef.current;
    if (!rail) return;
    const r = rail.getBoundingClientRect();
    const drag = { dx: e.clientX - r.left, dy: e.clientY - r.top };
    dragRef.current = drag;
    const move = (ev: PointerEvent) => {
      const host = railRef.current?.parentElement;
      if (!railRef.current || !host) return;
      const hr = host.getBoundingClientRect();
      const rr = railRef.current.getBoundingClientRect();
      const next = {
        x: clamp(ev.clientX - hr.left - drag.dx, 0, hr.width - rr.width),
        y: clamp(ev.clientY - hr.top - drag.dy, 0, hr.height - rr.height),
      };
      posRef.current = next;
      setPos(next);
    };
    const up = () => {
      endDragRef.current = null;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      dragRef.current = null;
      if (posRef.current) onPosChange?.(posRef.current);
    };
    endDragRef.current = up;
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    e.preventDefault();
  };

  const place: CSSProperties = pos
    ? { top: pos.y, left: pos.x }
    : { top: 8, left: "50%", transform: "translateX(-50%)" };

  return (
    <div ref={railRef} data-drawing-ui="true" onPointerDown={(e) => e.stopPropagation()}
      style={{ position: "absolute", ...place, zIndex: 6, display: "flex", flexDirection: "row", alignItems: "center", gap: 2,
        background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, padding: 3,
        font: `${TV_GEOM.uiFont}px ${TV_FONT}`, boxShadow: "0 2px 10px rgba(0,0,0,.12)" }}>
      <div aria-label="move toolbar" role="button" onPointerDown={onGripDown}
        style={{ width: 14, height: TV_GEOM.iconBtn, display: "grid", placeItems: "center", color: chrome.muted,
          cursor: dragRef.current ? "grabbing" : "grab", touchAction: "none" }}>
        <IconGrip size={14} />
      </div>

      <HoverButton aria-label="trend line" style={btn(activeTool === "trendline")} hoverStyle={toolHover(activeTool === "trendline")}
        onClick={() => toggleTool("trendline", activeTool === "trendline")}><IconTrend size={16} /></HoverButton>
      <HoverButton aria-label="horizontal line" style={btn(activeTool === "hline")} hoverStyle={toolHover(activeTool === "hline")}
        onClick={() => toggleTool("hline", activeTool === "hline")}><IconHLine size={16} /></HoverButton>
      <HoverButton aria-label="extended line" style={btn(activeTool === "extendedline")} hoverStyle={toolHover(activeTool === "extendedline")}
        onClick={() => toggleTool("extendedline", activeTool === "extendedline")}><IconExtended size={16} /></HoverButton>
      <HoverButton aria-label="rectangle" style={btn(activeTool === "rect")} hoverStyle={toolHover(activeTool === "rect")}
        onClick={() => toggleTool("rect", activeTool === "rect")}><IconRect size={16} /></HoverButton>
      <HoverButton aria-label="measure" style={btn(activeTool === "measure")} hoverStyle={toolHover(activeTool === "measure")}
        onClick={() => toggleTool("measure", activeTool === "measure")}><IconMeasure size={16} /></HoverButton>
      <div style={{ width: 1, height: 20, background: chrome.border, margin: "0 2px" }} />
      <HoverButton aria-label="hide all drawings" aria-pressed={hideAll} style={btn(hideAll)} hoverStyle={toolHover(hideAll)} onClick={onToggleHideAll}>
        {hideAll ? <IconEyeOff size={16} /> : <IconEye size={16} />}
      </HoverButton>
      <div style={{ position: "relative" }}>
        <HoverButton aria-label="delete" style={btn(false)} hoverStyle={toolHover(false)} onClick={onTrash}><IconTrash size={16} /></HoverButton>
        {confirm && (
          <div role="dialog" style={{ ...popover, left: "auto", right: 0, padding: 10, width: 200 }}>
            <div style={{ marginBottom: 8 }}>Clear all drawings for {bare}?</div>
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
              <HoverButton onClick={() => setConfirm(false)} hoverStyle={{ background: chrome.hover, color: chrome.text }}
                style={{ padding: "4px 10px", background: "transparent", border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" }}>Cancel</HoverButton>
              <HoverButton onClick={() => { setConfirm(false); onClearAll(); }} hoverStyle={{ background: chrome.hover, color: chrome.text }}
                style={{ padding: "4px 10px", background: chrome.down, border: "none", borderRadius: TV_GEOM.radius, color: "#fff", cursor: "pointer" }}>Clear</HoverButton>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
