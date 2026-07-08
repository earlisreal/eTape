// ui/src/chrome/panels/tv/TVToolbar.tsx
import type { CSSProperties } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import { IconIndicators, IconCamera, IconGear } from "./tvIcons";

export const TIMEFRAMES = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"] as const;

export interface TVToolbarProps {
  chrome: TvChrome; symbol: string; timeframe: string;
  onSymbolClick: () => void; onTimeframe: (tf: string) => void;
  onOpenIndicators: () => void; onScreenshot: () => void; onOpenSettings: () => void;
}

export function TVToolbar({ chrome, symbol, timeframe, onSymbolClick, onTimeframe, onOpenIndicators, onScreenshot, onOpenSettings }: TVToolbarProps): JSX.Element {
  const bare = symbol.replace(/^US\./, "");

  const iconBtn: CSSProperties = { width: TV_GEOM.iconBtn, height: TV_GEOM.iconBtn, display: "grid", placeItems: "center",
    background: "transparent", border: "none", borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" };
  const sep = <div style={{ width: 1, height: 20, background: chrome.border, margin: "0 4px" }} />;

  return (
    <div style={{ height: TV_GEOM.toolbarH, display: "flex", alignItems: "center", gap: 2, padding: "0 6px",
      borderBottom: `1px solid ${chrome.border}`, background: chrome.surface, color: chrome.text,
      font: `${TV_GEOM.uiFont}px ${TV_FONT}`, fontVariantNumeric: "tabular-nums" }}>
      <button aria-label={`symbol ${bare}`} onClick={onSymbolClick}
        style={{ ...iconBtn, width: "auto", padding: "0 8px", fontWeight: 700 }}>
        {bare}
      </button>
      {sep}
      {TIMEFRAMES.map((tf) => {
        const on = tf === timeframe;
        return (
          <button key={tf} aria-label={`timeframe ${tf}`} aria-pressed={on} onClick={() => onTimeframe(tf)}
            style={{ ...iconBtn, width: "auto", padding: "0 8px", fontWeight: on ? 700 : 500, color: on ? chrome.accent : chrome.text }}>
            {tf}
          </button>
        );
      })}
      {sep}
      <button aria-label="indicators" onClick={onOpenIndicators} style={{ ...iconBtn, width: "auto", gap: 6, padding: "0 8px" }}>
        <IconIndicators size={16} /> Indicators
      </button>
      <div style={{ flex: 1 }} />
      <button aria-label="screenshot" onClick={onScreenshot} style={iconBtn}><IconCamera size={16} /></button>
      <button aria-label="chart settings" onClick={onOpenSettings} style={iconBtn}><IconGear size={16} /></button>
    </div>
  );
}
