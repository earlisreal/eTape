// ui/src/chrome/panels/tv/ChartSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./TVDialog";
import type { TvChrome } from "../../../render/chart/tvTheme";

export interface ChartSettings { sessionShading: boolean; grid: boolean; watermark: boolean; barCloseTimer: boolean; visibleExtrema: boolean; volumeOverlap?: boolean }
export const DEFAULT_CHART_SETTINGS: ChartSettings = { sessionShading: true, grid: true, watermark: false, barCloseTimer: true, visibleExtrema: true };

export function normalizeChartSettings(raw: unknown): ChartSettings {
  const source = typeof raw === "object" && raw !== null ? raw as Partial<ChartSettings> : {};
  return {
    sessionShading: source.sessionShading !== false,
    grid: source.grid !== false,
    watermark: source.watermark === true,
    barCloseTimer: source.barCloseTimer !== false,
    visibleExtrema: source.visibleExtrema !== false,
    ...(typeof source.volumeOverlap === "boolean" ? { volumeOverlap: source.volumeOverlap } : {}),
  };
}

export function chartSettingsRollbackProjection(raw: unknown, settings: ChartSettings): Record<string, unknown> {
  const source = typeof raw === "object" && raw !== null ? raw as Record<string, unknown> : {};
  const result = { ...source, ...settings, volume: false };
  if (settings.volumeOverlap === undefined) delete result.volumeOverlap;
  return result;
}

export interface ChartSettingsDialogProps { chrome: TvChrome; settings: ChartSettings; globalVolumeOverlap?: boolean; onClose: () => void; onApply: (s: ChartSettings) => void }

const TOGGLES: { key: Exclude<keyof ChartSettings, "volumeOverlap">; label: string }[] = [
  { key: "sessionShading", label: "session shading" },
  { key: "grid", label: "grid" },
  { key: "watermark", label: "symbol watermark" },
  { key: "barCloseTimer", label: "bar-close timer" },
  { key: "visibleExtrema", label: "show visible high/low" },
];

export function ChartSettingsDialog({ chrome, settings, globalVolumeOverlap = false, onClose, onApply }: ChartSettingsDialogProps): JSX.Element {
  const [draft, setDraft] = useState<ChartSettings>({ ...settings });
  const row = { display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" } as const;
  return (
    <TVDialog title="Chart settings" chrome={chrome} onClose={onClose} width={300}
      footer={{ onOk: () => { onApply(draft); onClose(); } }}>
      {TOGGLES.map((t) => (
        <label key={t.key} style={row}>
          <span style={{ textTransform: "capitalize" }}>{t.label}</span>
          <input type="checkbox" aria-label={t.label} checked={draft[t.key]}
            onChange={(e) => setDraft((d) => ({ ...d, [t.key]: e.target.checked }))} />
        </label>
      ))}
      <label style={row}>
        <span>Overlap volume</span>
        <select aria-label="Overlap volume" value={draft.volumeOverlap === undefined ? "default" : draft.volumeOverlap ? "on" : "off"}
          onChange={(e) => setDraft((current) => {
            const next = { ...current };
            if (e.target.value === "default") delete next.volumeOverlap;
            else next.volumeOverlap = e.target.value === "on";
            return next;
          })}>
          <option value="default">Follow app default ({globalVolumeOverlap ? "On" : "Off"})</option>
          <option value="on">On</option>
          <option value="off">Off</option>
        </select>
      </label>
      <div style={{ ...row, borderTop: `1px solid ${chrome.border}`, marginTop: 4 }}>
        <span>Timezone</span>
        <span style={{ color: chrome.muted }}>ET</span>
      </div>
    </TVDialog>
  );
}
