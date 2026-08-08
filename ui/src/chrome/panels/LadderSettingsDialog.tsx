import { useState } from "react";
import { TVDialog } from "./tv/TVDialog";
import type { TvChrome } from "../../render/chart/tvTheme";
import {
  DEFAULT_LADDER_LEVELS,
  MAX_LADDER_LEVELS,
  MIN_LADDER_LEVELS,
  normalizeLadderLevels,
} from "../../render/ladder/ladderState";

export interface LadderSettingsDialogProps {
  chrome: TvChrome;
  levels: number;
  onClose: () => void;
  onApply: (levels: number) => void;
}

export function LadderSettingsDialog({ chrome, levels, onClose, onApply }: LadderSettingsDialogProps): JSX.Element {
  const [draft, setDraft] = useState<number>(levels);
  const row = { display: "flex", alignItems: "center", justifyContent: "space-between", padding: "8px 0" } as const;
  const numberInput = { width: 90, padding: "4px 6px", borderRadius: 4, border: `1px solid ${chrome.border}`, background: chrome.bg, color: chrome.text } as const;
  return (
    <TVDialog title="DOM Ladder settings" chrome={chrome} onClose={onClose} width={300}
      footer={{
        onDefaults: () => setDraft(DEFAULT_LADDER_LEVELS),
        onOk: () => { onApply(normalizeLadderLevels(draft)); onClose(); },
      }}>
      <label style={row}>
        <span>Depth levels</span>
        <input type="number" min={MIN_LADDER_LEVELS} max={MAX_LADDER_LEVELS} step={1}
          aria-label="depth levels" style={numberInput} value={draft}
          onChange={(e) => setDraft(Number(e.currentTarget.value))} />
      </label>
    </TVDialog>
  );
}
