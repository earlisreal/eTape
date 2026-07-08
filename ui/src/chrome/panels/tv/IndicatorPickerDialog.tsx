// ui/src/chrome/panels/tv/IndicatorPickerDialog.tsx
import { useMemo, useState } from "react";
import { TVDialog } from "./TVDialog";
import { INDICATOR_CATALOG, type IndicatorType } from "../../../render/chart/indicatorSeries";
import { TV_GEOM, type TvChrome } from "../../../render/chart/tvTheme";

export interface IndicatorPickerDialogProps { chrome: TvChrome; onClose: () => void; onAdd: (type: IndicatorType) => void }

export function IndicatorPickerDialog({ chrome, onClose, onAdd }: IndicatorPickerDialogProps): JSX.Element {
  const [q, setQ] = useState("");
  const entries = useMemo(() => Object.values(INDICATOR_CATALOG), []);
  const filtered = entries.filter((e) => e.label.toLowerCase().includes(q.trim().toLowerCase()));
  return (
    <TVDialog title="Indicators" chrome={chrome} onClose={onClose} width={320}>
      <div style={{ fontVariantNumeric: "tabular-nums" }}>
        <input placeholder="Search" value={q} onChange={(e) => setQ(e.target.value)} autoFocus
          style={{ width: "100%", boxSizing: "border-box", padding: "6px 8px", marginBottom: 8, background: chrome.bg,
            border: `1px solid ${chrome.border}`, borderRadius: TV_GEOM.radius, color: chrome.text }} />
        <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          {filtered.map((e) => (
            <button key={e.type} aria-label={`add ${e.label}`} onClick={() => { onAdd(e.type); onClose(); }}
              style={{ textAlign: "left", padding: "8px 10px", background: "transparent", border: "none", borderRadius: TV_GEOM.radius,
                color: chrome.text, cursor: "pointer" }}
              onMouseEnter={(ev) => (ev.currentTarget.style.background = chrome.hover)}
              onMouseLeave={(ev) => (ev.currentTarget.style.background = "transparent")}>
              {e.label}
            </button>
          ))}
          {filtered.length === 0 && <div style={{ color: chrome.muted, padding: "8px 10px" }}>No matches</div>}
        </div>
      </div>
    </TVDialog>
  );
}
