// ui/src/chrome/panels/tv/ChartSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./TVDialog";
import { TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";

export interface ChartSettings { sessionShading: boolean; grid: boolean; volume: boolean; watermark: boolean }
export const DEFAULT_CHART_SETTINGS: ChartSettings = { sessionShading: true, grid: true, volume: true, watermark: false };

export interface ChartSettingsDialogProps { chrome: TvChrome; settings: ChartSettings; onClose: () => void; onApply: (s: ChartSettings) => void }

const TOGGLES: { key: keyof ChartSettings; label: string }[] = [
  { key: "sessionShading", label: "session shading" },
  { key: "grid", label: "grid" },
  { key: "volume", label: "volume" },
  { key: "watermark", label: "symbol watermark" },
];

// A track + thumb pair, both using the shared TV_GEOM.radius (no alternate/pill radius) so the
// switch reads as the same flat, boxy TV chrome as every other rounded surface in this dialog.
// The native checkbox stays in the DOM (opacity 0, full-bleed) so labelling, click, and keyboard
// (space-to-toggle) semantics stay real rather than reimplemented.
function ToggleSwitch({ label, checked, onChange, chrome }: { label: string; checked: boolean; onChange: (v: boolean) => void; chrome: TvChrome }): JSX.Element {
  const w = 34;
  const h = 18;
  const pad = 2;
  const thumb = h - pad * 2;
  return (
    <span style={{ position: "relative", display: "inline-block", width: w, height: h, flexShrink: 0 }}>
      <input type="checkbox" aria-label={label} checked={checked} onChange={(e) => onChange(e.target.checked)}
        style={{ position: "absolute", inset: 0, margin: 0, opacity: 0, cursor: "pointer" }} />
      <span aria-hidden style={{ position: "absolute", inset: 0, borderRadius: TV_GEOM.radius,
        background: checked ? chrome.accent : chrome.border, pointerEvents: "none" }} />
      <span aria-hidden style={{ position: "absolute", top: pad, left: checked ? w - thumb - pad : pad, width: thumb, height: thumb,
        borderRadius: TV_GEOM.radius, background: "#fff", pointerEvents: "none" }} />
    </span>
  );
}

export function ChartSettingsDialog({ chrome, settings, onClose, onApply }: ChartSettingsDialogProps): JSX.Element {
  const [draft, setDraft] = useState<ChartSettings>({ ...settings });
  const row = { display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" } as const;
  return (
    <TVDialog title="Chart settings" chrome={chrome} onClose={onClose} width={300}
      footer={{ onOk: () => { onApply(draft); onClose(); } }}>
      {TOGGLES.map((t) => (
        <div key={t.key} style={row}>
          <span style={{ textTransform: "capitalize" }}>{t.label}</span>
          <ToggleSwitch label={t.label} checked={draft[t.key]} chrome={chrome}
            onChange={(v) => setDraft((d) => ({ ...d, [t.key]: v }))} />
        </div>
      ))}
      <div style={{ ...row, borderTop: `1px solid ${chrome.border}`, marginTop: 4 }}>
        <span>Timezone</span>
        <span style={{ color: chrome.muted }}>ET</span>
      </div>
    </TVDialog>
  );
}
