// ui/src/chrome/panels/tv/TVDrawingRail.tsx
import { useState } from "react";
import type { CSSProperties } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import type { Tool } from "../../../render/chart/drawings/interaction";
import { IconCursor, IconTrend, IconRay, IconHLine, IconHRay, IconRect, IconMeasure, IconMagnet, IconEye, IconEyeOff, IconTrash, IconCornerArrow } from "./tvIcons";

type LineTool = "trendline" | "ray" | "hline" | "hray";
const LINE_TOOLS: { tool: LineTool; label: string; Icon: (p: { size?: number }) => JSX.Element }[] = [
  { tool: "trendline", label: "Trend line", Icon: IconTrend },
  { tool: "ray", label: "Ray", Icon: IconRay },
  { tool: "hline", label: "Horizontal line", Icon: IconHLine },
  { tool: "hray", label: "Horizontal ray", Icon: IconHRay },
];

export interface TVDrawingRailProps {
  chrome: TvChrome; activeTool: Tool; magnet: boolean; hideAll: boolean; symbol: string;
  onSelectTool: (t: Tool) => void; onToggleMagnet: () => void; onToggleHideAll: () => void;
  hasSelection: () => boolean; onDeleteSelection: () => void; onClearAll: () => void;
}

export function TVDrawingRail({ chrome, activeTool, magnet, hideAll, symbol, onSelectTool, onToggleMagnet, onToggleHideAll, hasSelection, onDeleteSelection, onClearAll }: TVDrawingRailProps): JSX.Element {
  const [lastLine, setLastLine] = useState<LineTool>("trendline");
  const [flyout, setFlyout] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const bare = symbol.replace(/^US\./, "");

  const btn = (active: boolean): CSSProperties => ({ width: TV_GEOM.iconBtn, height: TV_GEOM.iconBtn, display: "grid",
    placeItems: "center", background: active ? chrome.hover : "transparent", border: "none", borderRadius: TV_GEOM.radius,
    color: active ? chrome.accent : chrome.text, cursor: "pointer" });
  const ActiveLine = LINE_TOOLS.find((l) => l.tool === lastLine)!.Icon;
  const lineActive = (["trendline", "ray", "hline", "hray"] as Tool[]).includes(activeTool);

  const pickLine = (t: LineTool) => { setLastLine(t); setFlyout(false); onSelectTool(t); };
  const onTrash = () => { if (hasSelection()) onDeleteSelection(); else setConfirm(true); };

  return (
    <div data-drawing-rail="true" onPointerDown={(e) => e.stopPropagation()}
      style={{ position: "absolute", top: 40, left: 6, zIndex: 6, display: "flex", flexDirection: "column", gap: 2,
        background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, padding: 3,
        font: `${TV_GEOM.uiFont}px ${TV_FONT}` }}>
      <button aria-label="cursor" style={btn(activeTool === "select")} onClick={() => onSelectTool("select")}><IconCursor size={16} /></button>

      <div style={{ position: "relative", display: "flex", alignItems: "center" }}>
        <button aria-label={`line tool ${lastLine}`} style={btn(lineActive)} onClick={() => onSelectTool(lastLine)}><ActiveLine size={16} /></button>
        <button aria-label="line tools" onClick={() => setFlyout((v) => !v)}
          style={{ position: "absolute", right: -1, bottom: -1, width: 12, height: 12, display: "grid", placeItems: "center",
            background: "transparent", border: "none", color: chrome.muted, cursor: "pointer" }}>
          <IconCornerArrow size={10} />
        </button>
        {flyout && (
          <div style={{ position: "absolute", left: TV_GEOM.iconBtn + 4, top: 0, zIndex: 20, background: chrome.surface,
            border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)", padding: 4, minWidth: 150 }}>
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

      <button aria-label="rectangle" style={btn(activeTool === "rect")} onClick={() => onSelectTool("rect")}><IconRect size={16} /></button>
      <button aria-label="measure" style={btn(activeTool === "measure")} onClick={() => onSelectTool("measure")}><IconMeasure size={16} /></button>
      <div style={{ height: 1, background: chrome.border, margin: "2px 0" }} />
      <button aria-label="magnet" aria-pressed={magnet} style={btn(magnet)} onClick={onToggleMagnet}><IconMagnet size={16} /></button>
      <button aria-label="hide all drawings" aria-pressed={hideAll} style={btn(hideAll)} onClick={onToggleHideAll}>
        {hideAll ? <IconEyeOff size={16} /> : <IconEye size={16} />}
      </button>
      <div style={{ position: "relative" }}>
        <button aria-label="delete" style={btn(false)} onClick={onTrash}><IconTrash size={16} /></button>
        {confirm && (
          <div role="dialog" style={{ position: "absolute", left: TV_GEOM.iconBtn + 4, top: 0, zIndex: 20, background: chrome.surface,
            border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)", padding: 10, width: 200 }}>
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
