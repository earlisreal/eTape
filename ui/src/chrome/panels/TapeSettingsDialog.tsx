// ui/src/chrome/panels/TapeSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./tv/TVDialog";
import type { TvChrome } from "../../render/chart/tvTheme";
import type { SignificanceStatus } from "../../wire/contract";

export interface TapeSettingsDialogProps {
  chrome: TvChrome;
  minSize: number;
  hoverEnabled: boolean;
  symbol: string;
  status?: SignificanceStatus;
  onClose: () => void;
  onApply: (minSize: number, hoverEnabled: boolean) => void;
}

export function TapeSettingsDialog({ chrome, minSize, hoverEnabled, symbol, status, onClose, onApply }: TapeSettingsDialogProps): JSX.Element {
  const [draft, setDraft] = useState<number>(minSize);
  const [hoverDraft, setHoverDraft] = useState(hoverEnabled);
  const row = { display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" } as const;
  const numberInput = { width: 90, padding: "4px 6px", borderRadius: 4, border: `1px solid ${chrome.border}`, background: chrome.bg, color: chrome.text } as const;
  const clamp = (n: number): number => Math.max(0, Math.floor(n) || 0);
  const readOnly = { color: chrome.muted, fontSize: 11 } as const;
  const threshold = (available: boolean | undefined, value: number | undefined): string => available ? `${value?.toLocaleString()}` : "warming";
  return (
    <TVDialog title="Time & Sales settings" chrome={chrome} onClose={onClose} width={300}
      footer={{ onDefaults: () => { setDraft(0); setHoverDraft(false); }, onOk: () => { onApply(clamp(draft), hoverDraft); onClose(); } }}>
      <label style={row}>
        <span>Minimum trade size</span>
        <input type="number" min={0} aria-label="minimum trade size" style={numberInput}
          value={draft} onChange={(e) => setDraft(Math.max(0, Number(e.target.value) || 0))} />
      </label>
      <label style={row}>
        <span>Show details on hover</span>
        <input type="checkbox" aria-label="show details on hover" checked={hoverDraft}
          onChange={(e) => setHoverDraft(e.target.checked)} />
      </label>
      <div aria-label="significant print status" style={{ borderTop: `1px solid ${chrome.border}`, marginTop: 6, paddingTop: 6 }}>
        <div style={{ ...row, paddingBottom: 2 }}><strong>Significant Print</strong><span style={readOnly}>{symbol || "—"}</span></div>
        <div style={{ ...row, padding: "3px 0" }}><span>State / pool</span><span style={readOnly}>{status?.state ?? "warming"} / {status?.pool ?? "—"}</span></div>
        <div style={{ ...row, padding: "3px 0" }}><span>Baseline</span><span style={readOnly}>{(status?.baselineCount ?? 0).toLocaleString()} / 2,000 {status?.full ? "(full)" : "(provisional)"}</span></div>
        <div style={{ ...row, padding: "3px 0" }}><span>Large threshold</span><span style={readOnly}>{threshold(status?.largeAvailable, status?.largeThreshold)}</span></div>
        <div style={{ ...row, paddingTop: 3 }}><span>Exceptional threshold</span><span style={readOnly}>{threshold(status?.exceptionalAvailable, status?.exceptionalThreshold)}</span></div>
        <p style={{ color: chrome.muted, fontSize: 11, lineHeight: 1.35, margin: "8px 0 0" }}>
          Significant Print compares share size with recent eligible prints for this symbol and session pool. Aggressor Direction describes the side that crossed the spread; it does not identify a buyer or seller.
        </p>
      </div>
    </TVDialog>
  );
}
