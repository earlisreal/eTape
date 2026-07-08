// ui/src/chrome/panels/tv/TVToolbar.tsx
import { useState } from "react";
import type { CSSProperties } from "react";
import { TV_FONT, TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";
import type { ChartType } from "../../../render/chart/chartTheme";
import { IconSearch, IconIndicators, IconCamera, IconGear, IconChevronDown, IconCandles, IconBars, IconLine, IconArea } from "./tvIcons";

export const TIMEFRAMES = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"] as const;

const TYPE_META: { type: ChartType; label: string; Icon: (p: { size?: number }) => JSX.Element }[] = [
  { type: "candle", label: "Candles", Icon: IconCandles },
  { type: "bar", label: "Bars", Icon: IconBars },
  { type: "line", label: "Line", Icon: IconLine },
  { type: "area", label: "Area", Icon: IconArea },
];

export interface TVToolbarProps {
  chrome: TvChrome; symbol: string; timeframe: string; chartType: ChartType;
  onSymbolClick: () => void; onTimeframe: (tf: string) => void; onChartType: (t: ChartType) => void;
  onOpenIndicators: () => void; onScreenshot: () => void; onOpenSettings: () => void;
}

export function TVToolbar({ chrome, symbol, timeframe, chartType, onSymbolClick, onTimeframe, onChartType, onOpenIndicators, onScreenshot, onOpenSettings }: TVToolbarProps): JSX.Element {
  const [typeOpen, setTypeOpen] = useState(false);
  const bare = symbol.replace(/^US\./, "");
  const active = TYPE_META.find((t) => t.type === chartType) ?? TYPE_META[0];

  const iconBtn: CSSProperties = { width: TV_GEOM.iconBtn, height: TV_GEOM.iconBtn, display: "grid", placeItems: "center",
    background: "transparent", border: "none", borderRadius: TV_GEOM.radius, color: chrome.text, cursor: "pointer" };
  const sep = <div style={{ width: 1, height: 20, background: chrome.border, margin: "0 4px" }} />;

  return (
    <div style={{ height: TV_GEOM.toolbarH, display: "flex", alignItems: "center", gap: 2, padding: "0 6px",
      borderBottom: `1px solid ${chrome.border}`, background: chrome.surface, color: chrome.text,
      font: `${TV_GEOM.uiFont}px ${TV_FONT}`, fontVariantNumeric: "tabular-nums" }}>
      <button aria-label={`symbol ${bare}`} onClick={onSymbolClick}
        style={{ ...iconBtn, width: "auto", gap: 6, padding: "0 8px", fontWeight: 700 }}>
        {bare} <IconSearch size={14} />
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
      <div style={{ position: "relative" }}>
        <button aria-label="chart type" onClick={() => setTypeOpen((v) => !v)} style={{ ...iconBtn, width: "auto", gap: 2, padding: "0 6px" }}>
          <active.Icon size={18} /> <IconChevronDown size={12} />
        </button>
        {typeOpen && (
          <div style={{ position: "absolute", top: TV_GEOM.iconBtn + 2, left: 0, zIndex: 20, background: chrome.surface,
            border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, boxShadow: "0 6px 20px rgba(0,0,0,.2)", padding: 4, minWidth: 120 }}>
            {TYPE_META.map((t) => (
              <button key={t.type} aria-label={`chart type ${t.type}`} onClick={() => { onChartType(t.type); setTypeOpen(false); }}
                style={{ display: "flex", alignItems: "center", gap: 8, width: "100%", padding: "6px 8px", background: "transparent",
                  border: "none", borderRadius: TV_GEOM.radius, color: t.type === chartType ? chrome.accent : chrome.text, cursor: "pointer" }}>
                <t.Icon size={16} /> {t.label}
              </button>
            ))}
          </div>
        )}
      </div>
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
