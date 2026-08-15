// ui/src/chrome/panels/tv/ChartHeaderControls.tsx
import { useRef, useState, type CSSProperties } from "react";
import type { Palette } from "../../../render/palette";
import type { IndicatorType } from "../../../render/chart/indicatorSeries";
import { IconIndicators, IconCamera, IconGear, IconTrend } from "./tvIcons";
import { IndicatorPickerPopover } from "./IndicatorPickerPopover";
import { HoverButton } from "../../controls/HoverButton";

export const TIMEFRAMES = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"] as const;

export interface ChartHeaderControlsProps {
  palette: Palette; timeframe: string;
  onTimeframe: (tf: string) => void;
  onAddIndicator: (type: IndicatorType) => void; onScreenshot: () => void; onOpenSettings: () => void;
  drawingToolsVisible: boolean; onToggleDrawingTools: () => void;
}

// Replaces the retired TVToolbar. That component was a second, self-contained 38px
// strip inside the chart panel body (its own symbol button, TvChrome/TV_GEOM tokens).
// This one portals into PanelFrame's ledger-header slot (see headerSlot.ts) so
// timeframe/indicators/screenshot/settings sit in the SAME row as the symbol the
// header already shows — no separate symbol button here, and styled with the app
// Daylight-Ledger palette + sans font so it reads as chrome, not canvas.
export function ChartHeaderControls(
  { palette, timeframe, onTimeframe, onAddIndicator, onScreenshot, onOpenSettings, drawingToolsVisible, onToggleDrawingTools }: ChartHeaderControlsProps,
): JSX.Element {
  const [pickerOpen, setPickerOpen] = useState(false);
  const indicatorsBtnRef = useRef<HTMLButtonElement | null>(null);
  const btn: CSSProperties = { display: "inline-flex", alignItems: "center", gap: 4,
    padding: "1px 6px", border: "none", background: "transparent", borderRadius: 3,
    color: palette.textMuted, cursor: "pointer", fontSize: 11,
    fontFamily: '"IBM Plex Sans", system-ui, sans-serif', fontVariantNumeric: "tabular-nums" };
  const iconBtn: CSSProperties = { ...btn, padding: 3 };
  const timeframeSelect: CSSProperties = { ...btn, display: undefined, padding: "1px 4px", fontWeight: 700, color: palette.accent };
  const sep = <div style={{ width: 1, height: 16, background: palette.border, margin: "0 4px", flex: "0 0 auto" }} />;

  return (
    <div className="chart-header-controls" style={{ display: "flex", alignItems: "center", gap: 2, flex: "1 1 auto", width: "100%", minWidth: 0, overflow: "hidden" }}>
      <div className="chart-header-timeframe-controls" style={{ display: "flex", alignItems: "center", gap: 2, flex: "1 1 auto", minWidth: 0, overflow: "hidden" }}>
        <div className="chart-header-timeframes" style={{ alignItems: "center", gap: 2, flex: "1 1 auto", minWidth: 0, overflow: "hidden" }}>
          {TIMEFRAMES.map((tf) => {
            const on = tf === timeframe;
            return (
              <HoverButton key={tf} type="button" aria-label={`timeframe ${tf}`} aria-pressed={on} onClick={() => onTimeframe(tf)}
                style={{ ...btn, fontWeight: on ? 700 : 500, color: on ? palette.accent : palette.textMuted }}
                hoverStyle={{ background: palette.surface, color: on ? palette.accent : palette.text }}>
                {tf}
              </HoverButton>
            );
          })}
        </div>
        <select className="chart-header-timeframe-select" aria-label="timeframe" value={timeframe}
          onChange={(e) => onTimeframe(e.currentTarget.value)} style={timeframeSelect}>
          {TIMEFRAMES.map((tf) => <option key={tf} value={tf}>{tf}</option>)}
        </select>
        {sep}
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 2, flex: "0 0 auto" }}>
        <HoverButton ref={indicatorsBtnRef} type="button" aria-label="indicators" title="Indicators" aria-haspopup="menu" aria-expanded={pickerOpen}
          onClick={() => setPickerOpen((v) => !v)} style={btn} hoverStyle={{ background: palette.surface, color: palette.text }}>
          <IconIndicators size={13} /> <span className="chart-header-indicators-label">Indicators</span>
        </HoverButton>
        {pickerOpen && (
          <IndicatorPickerPopover palette={palette} anchor={indicatorsBtnRef.current} onClose={() => setPickerOpen(false)}
            onAdd={(t) => { onAddIndicator(t); setPickerOpen(false); }} />
        )}
        <HoverButton type="button" aria-label="drawing tools" aria-pressed={drawingToolsVisible}
          title={drawingToolsVisible ? "Hide drawing tools" : "Show drawing tools"} onClick={onToggleDrawingTools}
          style={{ ...btn, fontWeight: drawingToolsVisible ? 700 : 500, color: drawingToolsVisible ? palette.accent : palette.textMuted }}
          hoverStyle={{ background: palette.surface, color: drawingToolsVisible ? palette.accent : palette.text }}>
          <IconTrend size={13} /> <span className="chart-header-drawings-label">Drawings</span>
        </HoverButton>
      </div>
      <span style={{ flex: 1 }} />
      <div style={{ display: "flex", alignItems: "center", gap: 2, flex: "0 0 auto" }}>
        <HoverButton type="button" aria-label="screenshot" onClick={onScreenshot} style={iconBtn}
          hoverStyle={{ background: palette.surface, color: palette.text }}><IconCamera size={14} /></HoverButton>
        <HoverButton type="button" aria-label="chart settings" onClick={onOpenSettings} style={iconBtn}
          hoverStyle={{ background: palette.surface, color: palette.text }}><IconGear size={14} /></HoverButton>
      </div>
    </div>
  );
}
