import { useState } from "react";
import { useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";
import {
  DEFAULT_SOUND_CONFIG,
  FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS,
  FILL_SOUND_LABELS, REJECT_SOUND_LABELS, SCANNER_SOUND_LABELS,
  type FillSoundId, type RejectSoundId, type ScannerSoundId,
} from "./SoundConfig";
import { useTheme } from "../chrome/ThemeProvider";

export function SoundsSection(): JSX.Element {
  const { config, save } = useSoundConfig();
  const { palette } = useTheme();
  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;
  const row = { display: "flex", gap: 6, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}` } as const;

  // remembered picks for re-checking after an unchecked (off) state
  const [lastPick, setLastPick] = useState<{ fill: FillSoundId; reject: RejectSoundId; scanner: ScannerSoundId }>({
    fill: config.fillSound === "off" ? (DEFAULT_SOUND_CONFIG.fillSound as FillSoundId) : config.fillSound,
    reject: config.rejectSound === "off" ? (DEFAULT_SOUND_CONFIG.rejectSound as RejectSoundId) : config.rejectSound,
    scanner: config.scannerSound === "off" ? (DEFAULT_SOUND_CONFIG.scannerSound as ScannerSoundId) : config.scannerSound,
  });
  const fillOn = config.fillSound !== "off";
  const rejectOn = config.rejectSound !== "off";
  const scannerOn = config.scannerSound !== "off";

  return (
    <div>
      <div className="col-head serif" style={{ marginBottom: 8 }}>Sounds</div>

      <label style={row}>
        <input data-testid="sound-enabled" type="checkbox" checked={config.enabled} onChange={(e) => save({ ...config, enabled: e.target.checked })} />
        <span>Enable sounds</span>
      </label>

      <div style={row}>
        <input data-testid="sound-fill-on" type="checkbox" checked={fillOn}
          onChange={(e) => save({ ...config, fillSound: e.target.checked ? lastPick.fill : "off" })} />
        <span style={{ width: 90 }}>Fill</span>
        <select data-testid="sound-fill" disabled={!fillOn} value={fillOn ? config.fillSound : lastPick.fill} style={inp}
          onChange={(e) => { const v = e.target.value as FillSoundId; setLastPick((p) => ({ ...p, fill: v })); save({ ...config, fillSound: v }); }}>
          {FILL_SOUND_IDS.map((id) => <option key={id} value={id}>{FILL_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-fill" disabled={!fillOn} style={{ ...inp, cursor: fillOn ? "pointer" : "not-allowed" }}
          onClick={() => soundEngine.preview("fill", fillOn ? config.fillSound : lastPick.fill)}>▶</button>
      </div>

      <label style={row}>
        <input data-testid="sound-place" type="checkbox" checked={config.placeClick} onChange={(e) => save({ ...config, placeClick: e.target.checked })} />
        <span>Placement click</span>
      </label>

      <div style={row}>
        <input data-testid="sound-reject-on" type="checkbox" checked={rejectOn}
          onChange={(e) => save({ ...config, rejectSound: e.target.checked ? lastPick.reject : "off" })} />
        <span style={{ width: 90 }}>Reject</span>
        <select data-testid="sound-reject" disabled={!rejectOn} value={rejectOn ? config.rejectSound : lastPick.reject} style={inp}
          onChange={(e) => { const v = e.target.value as RejectSoundId; setLastPick((p) => ({ ...p, reject: v })); save({ ...config, rejectSound: v }); }}>
          {REJECT_SOUND_IDS.map((id) => <option key={id} value={id}>{REJECT_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-reject" disabled={!rejectOn} style={{ ...inp, cursor: rejectOn ? "pointer" : "not-allowed" }}
          onClick={() => soundEngine.preview("reject", rejectOn ? config.rejectSound : lastPick.reject)}>▶</button>
      </div>

      <div style={row}>
        <input data-testid="sound-scanner-on" type="checkbox" checked={scannerOn}
          onChange={(e) => save({ ...config, scannerSound: e.target.checked ? lastPick.scanner : "off" })} />
        <span style={{ width: 90 }}>Scanner</span>
        <select data-testid="sound-scanner" disabled={!scannerOn} value={scannerOn ? config.scannerSound : lastPick.scanner} style={inp}
          onChange={(e) => { const v = e.target.value as ScannerSoundId; setLastPick((p) => ({ ...p, scanner: v })); save({ ...config, scannerSound: v }); }}>
          {SCANNER_SOUND_IDS.map((id) => <option key={id} value={id}>{SCANNER_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-scanner" disabled={!scannerOn} style={{ ...inp, cursor: scannerOn ? "pointer" : "not-allowed" }}
          onClick={() => soundEngine.preview("scanner", scannerOn ? config.scannerSound : lastPick.scanner)}>▶</button>
      </div>

      <div style={row}>
        <span style={{ width: 90 }}>Volume</span>
        <input data-testid="sound-volume" type="range" min={0} max={1} step={0.05} value={config.volume} onChange={(e) => save({ ...config, volume: Number(e.target.value) })} />
      </div>
    </div>
  );
}
