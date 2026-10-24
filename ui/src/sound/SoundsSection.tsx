import { useState } from "react";
import { useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";
import {
  DEFAULT_SOUND_CONFIG,
  FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS,
  FILL_SOUND_LABELS, REJECT_SOUND_LABELS, SCANNER_SOUND_LABELS,
  SESSION_VOICE_IDS, SESSION_VOICE_LABELS,
  type FillSoundId, type RejectSoundId, type ScannerSoundId,
} from "./SoundConfig";
import { useTheme } from "../chrome/ThemeProvider";
import { Button } from "../chrome/controls/Button";

export function SoundsSection(): JSX.Element {
  const { config, save } = useSoundConfig();
  const { palette } = useTheme();
  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.borderStrong}`, borderRadius: 4, fontSize: 12, padding: "5px 6px", width: "100%", minWidth: 0 } as const;

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

      <label className="sound-row" style={{ display: "flex" }}>
        <input data-testid="sound-enabled" type="checkbox" checked={config.enabled} onChange={(e) => save({ ...config, enabled: e.target.checked })} />
        <span>Enable sounds</span>
      </label>

      <div className="sound-row">
        <label><input data-testid="sound-session-on" type="checkbox" checked={config.sessionVoiceEnabled}
          onChange={(e) => save({ ...config, sessionVoiceEnabled: e.target.checked })} />
        Session voice</label>
        <select aria-label="Session voice" data-testid="sound-session-voice" disabled={!config.sessionVoiceEnabled} value={config.sessionVoice} style={inp}
          onChange={(e) => save({ ...config, sessionVoice: e.target.value as typeof config.sessionVoice })}>
          {SESSION_VOICE_IDS.map((id) => <option key={id} value={id}>{SESSION_VOICE_LABELS[id]}</option>)}
        </select>
        <Button iconOnly data-testid="sound-preview-session" aria-label="Preview session voice" title="Preview session voice" disabled={!config.sessionVoiceEnabled}
          onClick={() => soundEngine.previewSessionVoice()}>▶</Button>
      </div>

      <div className="sound-row">
        <label><input data-testid="sound-fill-on" type="checkbox" checked={fillOn}
          onChange={(e) => save({ ...config, fillSound: e.target.checked ? lastPick.fill : "off" })} />
        Fill</label>
        <select aria-label="Fill sound" data-testid="sound-fill" disabled={!fillOn} value={fillOn ? config.fillSound : lastPick.fill} style={inp}
          onChange={(e) => { const v = e.target.value as FillSoundId; setLastPick((p) => ({ ...p, fill: v })); save({ ...config, fillSound: v }); }}>
          {FILL_SOUND_IDS.map((id) => <option key={id} value={id}>{FILL_SOUND_LABELS[id]}</option>)}
        </select>
        <Button iconOnly aria-label="Preview fill sound" title="Preview fill sound" data-testid="sound-preview-fill" disabled={!fillOn}
          onClick={() => soundEngine.preview("fill", fillOn ? config.fillSound : lastPick.fill)}>▶</Button>
      </div>

      <label className="sound-row" style={{ display: "flex" }}>
        <input data-testid="sound-place" type="checkbox" checked={config.placeClick} onChange={(e) => save({ ...config, placeClick: e.target.checked })} />
        <span>Placement click</span>
      </label>

      <div className="sound-row">
        <label><input data-testid="sound-reject-on" type="checkbox" checked={rejectOn}
          onChange={(e) => save({ ...config, rejectSound: e.target.checked ? lastPick.reject : "off" })} />
        Reject</label>
        <select aria-label="Reject sound" data-testid="sound-reject" disabled={!rejectOn} value={rejectOn ? config.rejectSound : lastPick.reject} style={inp}
          onChange={(e) => { const v = e.target.value as RejectSoundId; setLastPick((p) => ({ ...p, reject: v })); save({ ...config, rejectSound: v }); }}>
          {REJECT_SOUND_IDS.map((id) => <option key={id} value={id}>{REJECT_SOUND_LABELS[id]}</option>)}
        </select>
        <Button iconOnly aria-label="Preview reject sound" title="Preview reject sound" data-testid="sound-preview-reject" disabled={!rejectOn}
          onClick={() => soundEngine.preview("reject", rejectOn ? config.rejectSound : lastPick.reject)}>▶</Button>
      </div>

      <div className="sound-row">
        <label><input data-testid="sound-scanner-on" type="checkbox" checked={scannerOn}
          onChange={(e) => save({ ...config, scannerSound: e.target.checked ? lastPick.scanner : "off" })} />
        Scanner</label>
        <select aria-label="Scanner sound" data-testid="sound-scanner" disabled={!scannerOn} value={scannerOn ? config.scannerSound : lastPick.scanner} style={inp}
          onChange={(e) => { const v = e.target.value as ScannerSoundId; setLastPick((p) => ({ ...p, scanner: v })); save({ ...config, scannerSound: v }); }}>
          {SCANNER_SOUND_IDS.map((id) => <option key={id} value={id}>{SCANNER_SOUND_LABELS[id]}</option>)}
        </select>
        <Button iconOnly aria-label="Preview scanner sound" title="Preview scanner sound" data-testid="sound-preview-scanner" disabled={!scannerOn}
          onClick={() => soundEngine.preview("scanner", scannerOn ? config.scannerSound : lastPick.scanner)}>▶</Button>
      </div>

      <div className="sound-row">
        <label htmlFor="sound-volume">Volume</label>
        <input id="sound-volume" data-testid="sound-volume" type="range" min={0} max={1} step={0.05} value={config.volume} onChange={(e) => save({ ...config, volume: Number(e.target.value) })} />
        <output htmlFor="sound-volume" style={{ fontSize: 10, color: palette.textMuted }}>{Math.round(config.volume * 100)}%</output>
      </div>
      <div className="sound-row" style={{ gridTemplateColumns: "1fr auto", borderBottom: 0 }}>
        <label><input data-testid="sound-price-alert-on" type="checkbox" checked={config.priceAlertEnabled} onChange={e=>save({...config,priceAlertEnabled:e.target.checked})}/>Price alert · Beacon</label>
        <Button iconOnly aria-label="Preview price alert sound" title="Preview price alert sound" disabled={!config.priceAlertEnabled} onClick={()=>soundEngine.previewPriceAlert()}>▶</Button>
      </div>
    </div>
  );
}
