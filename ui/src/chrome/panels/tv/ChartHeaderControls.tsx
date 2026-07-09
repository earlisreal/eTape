// ui/src/chrome/panels/tv/ChartHeaderControls.tsx
import type { CSSProperties } from "react";
import type { Palette } from "../../../render/palette";
import { IconIndicators, IconCamera, IconGear } from "./tvIcons";

export const TIMEFRAMES = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"] as const;

export interface ChartHeaderControlsProps {
  palette: Palette; timeframe: string;
  onTimeframe: (tf: string) => void;
  onOpenIndicators: () => void; onScreenshot: () => void; onOpenSettings: () => void;
}

// Replaces the retired TVToolbar. That component was a second, self-contained 38px
// strip inside the chart panel body (its own symbol button, TvChrome/TV_GEOM tokens).
// This one portals into PanelFrame's ledger-header slot (see headerSlot.ts) so
// timeframe/indicators/screenshot/settings sit in the SAME row as the symbol the
// header already shows — no separate symbol button here, and styled with the app
// Daylight-Ledger palette + sans font so it reads as chrome, not canvas.
export function ChartHeaderControls(
  { palette, timeframe, onTimeframe, onOpenIndicators, onScreenshot, onOpenSettings }: ChartHeaderControlsProps,
): JSX.Element {
  const btn: CSSProperties = { display: "inline-flex", alignItems: "center", gap: 4,
    padding: "1px 6px", border: "none", background: "transparent", borderRadius: 3,
    color: palette.textMuted, cursor: "pointer", fontSize: 11,
    fontFamily: '"IBM Plex Sans", system-ui, sans-serif', fontVariantNumeric: "tabular-nums" };
  const iconBtn: CSSProperties = { ...btn, padding: 3 };
  const sep = <div style={{ width: 1, height: 16, background: palette.border, margin: "0 4px", flex: "0 0 auto" }} />;

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 2, minWidth: 0, overflow: "hidden" }}>
      {TIMEFRAMES.map((tf) => {
        const on = tf === timeframe;
        return (
          <button key={tf} type="button" aria-label={`timeframe ${tf}`} aria-pressed={on} onClick={() => onTimeframe(tf)}
            style={{ ...btn, fontWeight: on ? 700 : 500, color: on ? palette.accent : palette.textMuted }}>
            {tf}
          </button>
        );
      })}
      {sep}
      <button type="button" aria-label="indicators" onClick={onOpenIndicators} style={btn}>
        <IconIndicators size={13} /> Indicators
      </button>
      <span style={{ flex: 1 }} />
      <button type="button" aria-label="screenshot" onClick={onScreenshot} style={iconBtn}><IconCamera size={14} /></button>
      <button type="button" aria-label="chart settings" onClick={onOpenSettings} style={iconBtn}><IconGear size={14} /></button>
    </div>
  );
}
