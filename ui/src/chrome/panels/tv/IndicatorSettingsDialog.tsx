// ui/src/chrome/panels/tv/IndicatorSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./TVDialog";
import { INDICATOR_CATALOG, withDefaultParams, type IndicatorInstance, type SeriesDescriptor, type SlotStyle } from "../../../render/chart/indicatorSeries";
import { LINE_STYLE_NAMES, type LineStyleName } from "../../../render/chart/lineStyle";
import { TV_FONT, TV_GEOM, TV_SWATCHES, type TvChrome } from "../../../render/chart/tvTheme";

export interface IndicatorSettingsDialogProps {
  chrome: TvChrome; instance: IndicatorInstance; resolved: SeriesDescriptor[];
  onClose: () => void; onApply: (next: IndicatorInstance) => void;
}

export function IndicatorSettingsDialog({ chrome, instance, resolved, onClose, onApply }: IndicatorSettingsDialogProps): JSX.Element {
  const entry = INDICATOR_CATALOG[instance.type];
  const [tab, setTab] = useState("Inputs");
  const [params, setParams] = useState<Record<string, number>>({ ...withDefaultParams(instance.type, instance.params) });
  const [styles, setStyles] = useState<Record<string, SlotStyle>>({ ...(instance.styles ?? {}) });

  const setStyle = (slot: string, patch: Partial<SlotStyle>) =>
    setStyles((s) => ({ ...s, [slot]: { ...s[slot], ...patch } }));

  const rowStyle = { display: "grid", gridTemplateColumns: "1fr auto", alignItems: "center", gap: 8, padding: "6px 0" } as const;
  // Number inputs carry numeral content (param values, line widths) — tabular-nums keeps
  // digits monospaced so they don't jitter as the value changes; radius uses the shared
  // TV_GEOM token so it stays in lockstep with every other rounded surface in the chrome.
  const numberInput = {
    background: chrome.bg, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text,
    padding: "4px 6px", font: `${TV_GEOM.uiFont}px ${TV_FONT}`, fontVariantNumeric: "tabular-nums",
  } as const;
  const selectInput = {
    background: chrome.bg, border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text,
    padding: "4px 6px", font: `${TV_GEOM.uiFont}px ${TV_FONT}`,
  } as const;
  // Preset swatch buttons (no native color wheel): same TV_SWATCHES palette the
  // drawing floating toolbar uses, so both style editors offer identical colors.
  const swatchBtn = (c: string, active: boolean) => ({
    width: 20, height: 20, padding: 0, borderRadius: TV_GEOM.radius, background: c, cursor: "pointer",
    border: active ? `2px solid ${chrome.accent}` : `1px solid ${chrome.border}`,
  } as const);

  const body = tab === "Inputs" ? (
    <div style={{ fontVariantNumeric: "tabular-nums" }}>
      {entry.params.length === 0 && <div style={{ color: chrome.muted }}>No inputs</div>}
      {entry.params.map((p) => (
        <div key={p.key} style={rowStyle}>
          <label htmlFor={`p-${p.key}`}>{p.label}</label>
          <input id={`p-${p.key}`} aria-label={p.label} type="number" min={p.min} max={p.max} style={numberInput}
            value={params[p.key]} onChange={(e) => setParams((prev) => ({ ...prev, [p.key]: Number(e.target.value) }))} />
        </div>
      ))}
    </div>
  ) : (
    <div>
      {entry.slots.map((s) => {
        const cur = resolved.find((r) => r.slot === s.slot);
        const color = styles[s.slot]?.color ?? cur?.color ?? chrome.accent;
        const width = styles[s.slot]?.width ?? cur?.width ?? 1;
        const ls = styles[s.slot]?.lineStyle ?? cur?.lineStyle ?? "solid";
        return (
          <div key={s.slot} style={{ borderBottom: `1px solid ${chrome.border}`, paddingBottom: 6, marginBottom: 6 }}>
            <div style={{ color: chrome.muted, marginBottom: 4 }}>{s.slot}</div>
            <div style={rowStyle}>
              <span>Color</span>
              <div role="group" aria-label={`${s.slot} color`} style={{ display: "flex", gap: 4 }}>
                {TV_SWATCHES.map((c) => {
                  const active = c.toUpperCase() === color.toUpperCase();
                  return (
                    <button key={c} aria-label={`${s.slot} color ${c}`} aria-pressed={active}
                      onClick={() => setStyle(s.slot, { color: c })} style={swatchBtn(c, active)} />
                  );
                })}
              </div>
            </div>
            <div style={rowStyle}>
              <span>Width</span>
              <input aria-label={`${s.slot} width`} type="number" min={1} max={4} style={numberInput} value={width}
                onChange={(e) => setStyle(s.slot, { width: Number(e.target.value) })} />
            </div>
            <div style={rowStyle}>
              <span>Line</span>
              <select aria-label={`${s.slot} style`} style={selectInput} value={ls}
                onChange={(e) => setStyle(s.slot, { lineStyle: e.target.value as LineStyleName })}>
                {LINE_STYLE_NAMES.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
            </div>
          </div>
        );
      })}
    </div>
  );

  return (
    <TVDialog title={entry.label} chrome={chrome} onClose={onClose} width={320}
      tabs={["Inputs", "Style"]} activeTab={tab} onTab={setTab}
      footer={{
        onDefaults: () => { setParams(withDefaultParams(instance.type, {})); setStyles({}); },
        onOk: () => { onApply({ ...instance, params, styles }); onClose(); },
      }}>
      {body}
    </TVDialog>
  );
}
