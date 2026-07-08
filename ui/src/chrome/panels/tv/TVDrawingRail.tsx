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
import { IconGrip, IconTrend, IconRay, IconHLine, IconHRay, IconRect, IconMeasure, IconMagnet, IconEye, IconEyeOff, IconTrash, IconCornerArrow } from "./tvIcons";

type LineTool = "trendline" | "ray" | "hline" | "hray";
const LINE_TOOLS: { tool: LineTool; label: string; Icon: (p: { size?: number }) => JSX.Element }[] = [
  { tool: "trendline", label: "Trend line", Icon: IconTrend },
  { tool: "ray", label: "Ray", Icon: IconRay },
  { tool: "hline", label: "Horizontal line", Icon: IconHLine },
  { tool: "hray", label: "Horizontal ray", Icon: IconHRay },
];

export interface RailPos { x: number; y: number }

export interface TVDrawingRailProps {
  chrome: TvChrome; activeTool: Tool; magnet: boolean; hideAll: boolean; symbol: string;
  onSelectTool: (t: Tool) => void; onToggleMagnet: () => void; onToggleHideAll: () => void;
  hasSelection: () => boolean; onDeleteSelection: () => void; onClearAll: () => void;
  initialPos?: RailPos | null; onPosChange?: (p: RailPos) => void;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), Math.max(lo, hi));

export function TVDrawingRail({ chrome, activeTool, magnet, hideAll, symbol, onSelectTool, onToggleMagnet, onToggleHideAll, hasSelection, onDeleteSelection, onClearAll, initialPos, onPosChange }: TVDrawingRailProps): JSX.Element {
  const [lastLine, setLastLine] = useState<LineTool>("trendline");
  const [flyout, setFlyout] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [pos, setPos] = useState<RailPos | null>(initialPos ?? null);
  const railRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ dx: number; dy: number } | null>(null);
  const posRef = useRef<RailPos | null>(pos);
  const bare = symbol.replace(/^US\./, "");

  const btn = (active: boolean): CSSProperties => ({ width: TV_GEOM.iconBtn, height: TV_GEOM.iconBtn, display: "grid",
    placeItems: "center", background: active ? chrome.hover : "transparent", border: "none", borderRadius: TV_GEOM.radius,
    color: active ? chrome.accent : chrome.text, cursor: "pointer" });
  const ActiveLine = LINE_TOOLS.find((l) => l.tool === lastLine)!.Icon;
  const lineActive = (["trendline", "ray", "hline", "hray"] as Tool[]).includes(activeTool);
  // Popovers hang below the horizontal bar (they used to fly out to the right of
  // the old vertical rail).
  const popover: CSSProperties = { position: "absolute", left: 0, top: TV_GEOM.iconBtn + 6, zIndex: 20, background: chrome.surface,
    border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)", padding: 4 };

  const pickLine = (t: LineTool) => { setLastLine(t); setFlyout(false); onSelectTool(t); };
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

      <div style={{ position: "relative", display: "flex", alignItems: "center" }}>
        <button aria-label={`line tool ${lastLine}`} style={btn(lineActive)} onClick={() => toggleTool(lastLine, lineActive)}><ActiveLine size={16} /></button>
        <button aria-label="line tools" onClick={() => setFlyout((v) => !v)}
          style={{ position: "absolute", right: -1, bottom: -1, width: 12, height: 12, display: "grid", placeItems: "center",
            background: "transparent", border: "none", color: chrome.muted, cursor: "pointer" }}>
          <IconCornerArrow size={10} />
        </button>
        {flyout && (
          <div style={{ ...popover, minWidth: 150 }}>
            {LINE_TOOLS.map((l) => (
              <button key={l.tool} aria-label={`select ${l.tool}`} onClick={() => pickLine(l.tool)}
                style={{ display: "flex", alignItems: "center", gap: 8, width: "100%", padding: "6px 8px", background: "transparent",
                  border: "none", borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" }}>
                <l.Icon size={16} /> {l.label}
              </button>
            ))}
          </div>
        )}
      </div>

      <button aria-label="rectangle" style={btn(activeTool === "rect")} onClick={() => toggleTool("rect", activeTool === "rect")}><IconRect size={16} /></button>
      <button aria-label="measure" style={btn(activeTool === "measure")} onClick={() => toggleTool("measure", activeTool === "measure")}><IconMeasure size={16} /></button>
      <div style={{ width: 1, height: 20, background: chrome.border, margin: "0 2px" }} />
      <button aria-label="magnet" aria-pressed={magnet} style={btn(magnet)} onClick={onToggleMagnet}><IconMagnet size={16} /></button>
      <button aria-label="hide all drawings" aria-pressed={hideAll} style={btn(hideAll)} onClick={onToggleHideAll}>
        {hideAll ? <IconEyeOff size={16} /> : <IconEye size={16} />}
      </button>
      <div style={{ position: "relative" }}>
        <button aria-label="delete" style={btn(false)} onClick={onTrash}><IconTrash size={16} /></button>
        {confirm && (
          <div role="dialog" style={{ ...popover, left: "auto", right: 0, padding: 10, width: 200 }}>
            <div style={{ marginBottom: 8 }}>Clear all drawings for {bare}?</div>
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
              <button onClick={() => setConfirm(false)} style={{ padding: "4px 10px", background: "transparent", border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" }}>Cancel</button>
              <button onClick={() => { setConfirm(false); onClearAll(); }} style={{ padding: "4px 10px", background: chrome.down, border: "none", borderRadius: TV_GEOM.radius, color: "#fff", cursor: "pointer" }}>Clear</button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
