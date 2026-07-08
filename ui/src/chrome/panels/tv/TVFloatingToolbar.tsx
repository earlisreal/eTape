// ui/src/chrome/panels/tv/TVFloatingToolbar.tsx
import { useState } from "react";
import { TV_FONT, TV_GEOM, TV_SWATCHES, type TvChrome } from "../../../render/chart/tvTheme";
import { LINE_STYLE_NAMES, type LineStyleName } from "../../../render/chart/lineStyle";
import { IconClone, IconTrash } from "./tvIcons";

export interface TVFloatingToolbarProps {
  chrome: TvChrome; rect: { x: number; y: number; w: number; h: number };
  color: string; width: number; lineStyle: LineStyleName;
  onColor: (c: string) => void; onWidth: (w: number) => void; onLineStyle: (s: LineStyleName) => void;
  onClone: () => void; onDelete: () => void;
}

export function TVFloatingToolbar({ chrome, rect, color, width, lineStyle, onColor, onWidth, onLineStyle, onClone, onDelete }: TVFloatingToolbarProps): JSX.Element {
  const [palette, setPalette] = useState(false);
  // The width control (1-4) carries numeral content — tabular-nums keeps digits from
  // jittering, matching the convention set in TVToolbar/IndicatorSettingsDialog. Radius
  // uses the shared TV_GEOM token so every rounded surface here stays in lockstep with
  // the rest of the TV chrome (pill, swatch, popover, buttons all share one token).
  const iconBtn = { width: 24, height: 24, display: "grid", placeItems: "center", background: "transparent", border: "none", borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" } as const;

  return (
    // data-drawing-ui: tells DrawingInteraction's raw pointerdown listener on the
    // chart host to ignore this subtree — otherwise the pointerdown deselects the
    // drawing and unmounts this toolbar before any button's click can fire.
    <div data-drawing-ui="true" style={{ position: "absolute", left: rect.x + rect.w / 2, top: Math.max(4, rect.y - 40), transform: "translateX(-50%)",
      zIndex: 8, display: "flex", alignItems: "center", gap: 4, padding: "4px 6px", background: chrome.surface,
      border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 4px 16px rgba(0,0,0,.22)", font: `${TV_GEOM.uiFont}px ${TV_FONT}`, fontVariantNumeric: "tabular-nums" }}>
      <div style={{ position: "relative" }}>
        <button aria-label="color" onClick={() => setPalette((v) => !v)}
          style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: color, cursor: "pointer" }} />
        {palette && (
          <div style={{ position: "absolute", top: 26, left: 0, zIndex: 20, display: "grid", gridTemplateColumns: "repeat(4, 20px)", gap: 4,
            padding: 6, background: chrome.surface, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)" }}>
            {TV_SWATCHES.map((c) => (
              <button key={c} aria-label={`color ${c}`} onClick={() => { onColor(c); setPalette(false); }}
                style={{ width: 20, height: 20, borderRadius: TV_GEOM.radius, border: `1px solid ${chrome.border}`, background: c, cursor: "pointer" }} />
            ))}
          </div>
        )}
      </div>
      {[1, 2, 3, 4].map((w) => (
        <button key={w} aria-label={`width ${w}`} onClick={() => onWidth(w)}
          style={{ ...iconBtn, width: 22, color: w === width ? chrome.accent : chrome.text, fontWeight: w === width ? 700 : 500 }}>{w}</button>
      ))}
      <select aria-label="line style" value={lineStyle} onChange={(e) => onLineStyle(e.target.value as LineStyleName)}
        style={{ background: chrome.bg, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text, padding: "2px 4px" }}>
        {LINE_STYLE_NAMES.map((n) => <option key={n} value={n}>{n}</option>)}
      </select>
      <button aria-label="clone" onClick={onClone} style={iconBtn}><IconClone size={15} /></button>
      <button aria-label="delete drawing" onClick={onDelete} style={iconBtn}><IconTrash size={15} /></button>
    </div>
  );
}
