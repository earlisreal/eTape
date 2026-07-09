// ui/src/chrome/panels/TapeSettingsDialog.tsx
import { useState } from "react";
import { TVDialog } from "./tv/TVDialog";
import type { TvChrome } from "../../render/chart/tvTheme";

export interface TapeSettingsDialogProps { chrome: TvChrome; minSize: number; onClose: () => void; onApply: (minSize: number) => void }

export function TapeSettingsDialog({ chrome, minSize, onClose, onApply }: TapeSettingsDialogProps): JSX.Element {
  const [draft, setDraft] = useState<number>(minSize);
  const row = { display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" } as const;
  const numberInput = { width: 90, padding: "4px 6px", borderRadius: 4, border: `1px solid ${chrome.border}`, background: chrome.bg, color: chrome.text } as const;
  const clamp = (n: number): number => Math.max(0, Math.floor(n) || 0);
  return (
    <TVDialog title="Time & Sales settings" chrome={chrome} onClose={onClose} width={300}
      footer={{ onDefaults: () => setDraft(0), onOk: () => { onApply(clamp(draft)); onClose(); } }}>
      <label style={row}>
        <span>Minimum trade size</span>
        <input type="number" min={0} aria-label="minimum trade size" style={numberInput}
          value={draft} onChange={(e) => setDraft(Math.max(0, Number(e.target.value) || 0))} />
      </label>
    </TVDialog>
  );
}
