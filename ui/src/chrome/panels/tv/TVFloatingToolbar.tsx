// ui/src/chrome/panels/tv/TVFloatingToolbar.tsx
import { useEffect, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { TV_FONT, TV_GEOM, TV_SWATCHES, type TvChrome } from "../../../render/chart/tvTheme";
import type { DrawingKind } from "../../../render/chart/drawings/model";
import { LINE_STYLE_NAMES, type LineStyleName } from "../../../render/chart/lineStyle";
import { IconArea, IconClone, IconGrip, IconTrash } from "./tvIcons";
import { HoverButton } from "../../controls/HoverButton";

export interface TVFloatingToolbarProps {
  chrome: TvChrome; kind: DrawingKind; rect: { x: number; y: number; w: number; h: number };
  color: string; width: number; lineStyle: LineStyleName;
  fill: boolean; fillColor: string; fillOpacity: number;
  onColor: (c: string) => void; onWidth: (w: number) => void; onLineStyle: (s: LineStyleName) => void;
  onFill: (fill: boolean) => void; onFillColor: (c: string) => void; onFillOpacity: (opacity: number) => void;
  onClone: () => void; onDelete: () => void;
}

interface ToolbarPos { x: number; y: number }

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), Math.max(lo, hi));

export function TVFloatingToolbar({ chrome, kind, rect, color, width, lineStyle, fill, fillColor, fillOpacity, onColor, onWidth, onLineStyle, onFill, onFillColor, onFillOpacity, onClone, onDelete }: TVFloatingToolbarProps): JSX.Element {
  const [palette, setPalette] = useState<"outline" | "fill" | null>(null);
  const [pos, setPos] = useState<ToolbarPos>({ x: rect.x + rect.w / 2, y: Math.max(4, rect.y - 40) });
  const toolbarRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ dx: number; dy: number } | null>(null);
  const posRef = useRef<ToolbarPos>(pos);
  const endDragRef = useRef<(() => void) | null>(null);
  // The width control (1-4) carries numeral content — tabular-nums keeps digits from
  // jittering, matching the convention set in ChartHeaderControls/IndicatorSettingsDialog.
  // Radius uses the shared TV_GEOM token so every rounded surface here stays in
  // lockstep with the rest of the TV chrome (pill, swatch, popover, buttons all share
  // one token).
  const iconBtn = { width: 24, height: 24, display: "grid", placeItems: "center", background: "transparent", border: "none", borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" } as const;
  const iconHover = { background: chrome.hover, color: chrome.text };
  // Swatches' background IS the color they represent — hover must not swap it
  // out, so use an inset ring instead of the standard background/color overlay.
  const swatchHover = { boxShadow: `inset 0 0 0 2px ${chrome.hover}` };

  useEffect(() => () => endDragRef.current?.(), []);
  const onGripDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    const toolbar = toolbarRef.current;
    if (!toolbar) return;
    const r = toolbar.getBoundingClientRect();
    const drag = { dx: e.clientX - r.left, dy: e.clientY - r.top };
    dragRef.current = drag;
    const move = (ev: PointerEvent) => {
      const host = toolbarRef.current?.parentElement;
      if (!toolbarRef.current || !host) return;
      const hr = host.getBoundingClientRect();
      const rr = toolbarRef.current.getBoundingClientRect();
      const next = {
        x: clamp(ev.clientX - hr.left - drag.dx + rr.width / 2, rr.width / 2, hr.width - rr.width / 2),
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
    };
    endDragRef.current = up;
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    e.preventDefault();
  };

  return (
    // data-drawing-ui: tells DrawingInteraction's raw pointerdown listener on the
    // chart host to ignore this subtree — otherwise the pointerdown deselects the
    // drawing and unmounts this toolbar before any button's click can fire.
    <div ref={toolbarRef} data-drawing-ui="true" onPointerDown={(e) => e.stopPropagation()}
      style={{ position: "absolute", left: pos.x, top: pos.y, transform: "translateX(-50%)",
      zIndex: 8, display: "flex", alignItems: "center", gap: 4, padding: "4px 6px", background: chrome.surface,
      border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 4px 16px rgba(0,0,0,.22)", font: `${TV_GEOM.uiFont}px ${TV_FONT}`, fontVariantNumeric: "tabular-nums" }}>
      <div aria-label="move toolbar" title="Move toolbar" role="button" onPointerDown={onGripDown}
        style={{ width: 14, height: 24, display: "grid", placeItems: "center", color: chrome.muted,
          cursor: dragRef.current ? "grabbing" : "grab", touchAction: "none" }}>
        <IconGrip size={14} />
      </div>
      <div style={{ position: "relative" }}>
        <HoverButton aria-label="color" title="Color" onClick={() => setPalette((v) => v === "outline" ? null : "outline")} hoverStyle={swatchHover}
          style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: color, cursor: "pointer" }} />
        {palette === "outline" && (
          <div style={{ position: "absolute", top: 26, left: 0, zIndex: 20, display: "grid", gridTemplateColumns: "repeat(4, 20px)", gap: 4,
            padding: 6, background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)" }}>
            {TV_SWATCHES.map((c) => (
              <HoverButton key={c} aria-label={`color ${c}`} title={`Color ${c}`} onClick={() => { onColor(c); setPalette(null); }} hoverStyle={swatchHover}
                style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: c, cursor: "pointer" }} />
            ))}
          </div>
        )}
      </div>
      {[1, 2, 3, 4].map((w) => (
        <HoverButton key={w} aria-label={`width ${w}`} title={`Width ${w}`} onClick={() => onWidth(w)} hoverStyle={iconHover}
          style={{ ...iconBtn, width: 22, color: w === width ? chrome.accent : chrome.text, fontWeight: w === width ? 700 : 500 }}>{w}</HoverButton>
      ))}
      <select aria-label="line style" title="Line style" value={lineStyle} onChange={(e) => onLineStyle(e.target.value as LineStyleName)}
        style={{ background: chrome.bg, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text, padding: "2px 4px" }}>
        {LINE_STYLE_NAMES.map((n) => <option key={n} value={n}>{n}</option>)}
      </select>
      {kind === "rect" && <>
        <div style={{ width: 1, height: 20, background: chrome.border, margin: "0 2px" }} />
        <HoverButton aria-label="fill" title={fill ? "Disable fill" : "Enable fill"} aria-pressed={fill}
          onClick={() => onFill(!fill)} hoverStyle={iconHover}
          style={{ ...iconBtn, color: fill ? chrome.accent : chrome.text, background: fill ? chrome.hover : "transparent",
            boxShadow: fill ? `inset 0 0 0 1px ${chrome.accent}` : "none" }}><IconArea size={15} /></HoverButton>
        <div style={{ position: "relative" }}>
          <HoverButton aria-label="fill color" title="Fill color" onClick={() => setPalette((v) => v === "fill" ? null : "fill")} hoverStyle={swatchHover}
            style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: fillColor, cursor: "pointer" }} />
          {palette === "fill" && (
            <div style={{ position: "absolute", top: 26, left: 0, zIndex: 20, display: "grid", gridTemplateColumns: "repeat(4, 20px)", gap: 4,
              padding: 6, background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)" }}>
              {TV_SWATCHES.map((c) => (
                <HoverButton key={c} aria-label={`fill color ${c}`} title={`Fill color ${c}`} onClick={() => { onFillColor(c); setPalette(null); }} hoverStyle={swatchHover}
                  style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: c, cursor: "pointer" }} />
              ))}
            </div>
          )}
        </div>
        <label title="Fill opacity" style={{ display: "flex", alignItems: "center", gap: 3, color: chrome.muted, fontSize: 10 }}>
          <input aria-label="fill opacity" type="range" min={0} max={100} step={5} value={fillOpacity}
            onChange={(e) => onFillOpacity(Number(e.target.value))} style={{ width: 58, accentColor: chrome.accent }} />
          <span>{fillOpacity}%</span>
        </label>
      </>}
      <HoverButton aria-label="clone" title="Clone" onClick={onClone} style={iconBtn} hoverStyle={iconHover}><IconClone size={15} /></HoverButton>
      <HoverButton aria-label="delete drawing" title="Delete" onClick={onDelete} style={iconBtn} hoverStyle={iconHover}><IconTrash size={15} /></HoverButton>
    </div>
  );
}
