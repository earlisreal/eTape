// ui/src/chrome/panels/tv/ChartSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./TVDialog";
import type { TvChrome } from "../../../render/chart/tvTheme";

export interface ChartSettings { sessionShading: boolean; grid: boolean; watermark: boolean; barCloseTimer: boolean; visibleExtrema: boolean }
export const DEFAULT_CHART_SETTINGS: ChartSettings = { sessionShading: true, grid: true, watermark: false, barCloseTimer: true, visibleExtrema: true };

export function normalizeChartSettings(raw: unknown): ChartSettings {
  const source = typeof raw === "object" && raw !== null ? raw as Partial<ChartSettings> : {};
  return {
    sessionShading: source.sessionShading !== false,
    grid: source.grid !== false,
    watermark: source.watermark === true,
    barCloseTimer: source.barCloseTimer !== false,
    visibleExtrema: source.visibleExtrema !== false,
  };
}

export function chartSettingsRollbackProjection(raw: unknown, settings: ChartSettings): Record<string, unknown> {
  const source = typeof raw === "object" && raw !== null ? raw as Record<string, unknown> : {};
  return { ...source, ...settings, volume: false };
}

export interface ChartSettingsDialogProps { chrome: TvChrome; settings: ChartSettings; onClose: () => void; onApply: (s: ChartSettings) => void }

const TOGGLES: { key: keyof ChartSettings; label: string }[] = [
  { key: "sessionShading", label: "session shading" },
  { key: "grid", label: "grid" },
  { key: "watermark", label: "symbol watermark" },
  { key: "barCloseTimer", label: "bar-close timer" },
  { key: "visibleExtrema", label: "show visible high/low" },
];

export function ChartSettingsDialog({ chrome, settings, onClose, onApply }: ChartSettingsDialogProps): JSX.Element {
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
      <div style={{ ...row, borderTop: `1px solid ${chrome.border}`, marginTop: 4 }}>
        <span>Timezone</span>
        <span style={{ color: chrome.muted }}>ET</span>
      </div>
    </TVDialog>
  );
}
