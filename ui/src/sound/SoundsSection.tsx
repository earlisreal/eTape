import { useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";
import {
  DEFAULT_SOUND_CONFIG,
  FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS,
  FILL_SOUND_LABELS, REJECT_SOUND_LABELS, SCANNER_SOUND_LABELS,
} from "./SoundConfig";
import { useTheme } from "../chrome/ThemeProvider";

export function SoundsSection(): JSX.Element {
  const { config, save } = useSoundConfig();
  const { palette } = useTheme();
  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;
  const row = { display: "flex", gap: 6, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}` } as const;

  return (
    <div>
      <div style={{ fontWeight: 700, marginTop: 8 }}>Sounds</div>

      <label style={row}>
        <input data-testid="sound-enabled" type="checkbox" checked={config.enabled} onChange={(e) => save({ ...config, enabled: e.target.checked })} />
        <span>Enable sounds</span>
      </label>

      <div style={row}>
        <span style={{ width: 90 }}>Fill</span>
        <select data-testid="sound-fill" value={config.fillSound} style={inp} onChange={(e) => save({ ...config, fillSound: e.target.value as typeof config.fillSound })}>
          <option value="off">off</option>
          {FILL_SOUND_IDS.map((id) => <option key={id} value={id}>{FILL_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-fill" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("fill", config.fillSound === "off" ? DEFAULT_SOUND_CONFIG.fillSound : config.fillSound)}>▶</button>
      </div>

      <label style={row}>
        <input data-testid="sound-place" type="checkbox" checked={config.placeClick} onChange={(e) => save({ ...config, placeClick: e.target.checked })} />
        <span>Placement click</span>
      </label>

      <div style={row}>
        <span style={{ width: 90 }}>Reject</span>
        <select data-testid="sound-reject" value={config.rejectSound} style={inp} onChange={(e) => save({ ...config, rejectSound: e.target.value as typeof config.rejectSound })}>
          <option value="off">off</option>
          {REJECT_SOUND_IDS.map((id) => <option key={id} value={id}>{REJECT_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-reject" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("reject", config.rejectSound === "off" ? DEFAULT_SOUND_CONFIG.rejectSound : config.rejectSound)}>▶</button>
      </div>

      <div style={row}>
        <span style={{ width: 90 }}>Scanner</span>
        <select data-testid="sound-scanner" value={config.scannerSound} style={inp} onChange={(e) => save({ ...config, scannerSound: e.target.value as typeof config.scannerSound })}>
          <option value="off">off</option>
          {SCANNER_SOUND_IDS.map((id) => <option key={id} value={id}>{SCANNER_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-scanner" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("scanner", config.scannerSound === "off" ? DEFAULT_SOUND_CONFIG.scannerSound : config.scannerSound)}>▶</button>
      </div>

      <div style={row}>
        <span style={{ width: 90 }}>Volume</span>
        <input data-testid="sound-volume" type="range" min={0} max={1} step={0.05} value={config.volume} onChange={(e) => save({ ...config, volume: Number(e.target.value) })} />
      </div>
    </div>
  );
}
