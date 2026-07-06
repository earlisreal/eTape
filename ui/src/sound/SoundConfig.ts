// Sound settings. Persisted engine-side under the generic KV command as key
// "soundConfig" (separate from "orderConfig" so each schema stays independent).
export type FillSoundId = "softBlip" | "twoTone" | "marimba" | "cashBell" | "tick" | "glassPing" | "pop";
export type RejectSoundId = "buzz" | "dunDun" | "doubleKnock" | "alertBeeps" | "powerDown";
export type ScannerSoundId = "sonarPing" | "arpeggio" | "chirp" | "highChime" | "singingBowl";

export interface SoundConfig {
  enabled: boolean;                     // master
  volume: number;                       // 0..1
  fillSound: FillSoundId | "off";
  placeClick: boolean;
  rejectSound: RejectSoundId | "off";
  scannerSound: ScannerSoundId | "off";
}

export const SOUND_CONFIG_KEY = "soundConfig";

export const DEFAULT_SOUND_CONFIG: SoundConfig = {
  enabled: true,
  volume: 0.6,
  fillSound: "twoTone",
  placeClick: true,
  rejectSound: "alertBeeps",
  scannerSound: "arpeggio",
};

export const FILL_SOUND_IDS: readonly FillSoundId[] = ["softBlip", "twoTone", "marimba", "cashBell", "tick", "glassPing", "pop"];
export const REJECT_SOUND_IDS: readonly RejectSoundId[] = ["buzz", "dunDun", "doubleKnock", "alertBeeps", "powerDown"];
export const SCANNER_SOUND_IDS: readonly ScannerSoundId[] = ["sonarPing", "arpeggio", "chirp", "highChime", "singingBowl"];

// Dropdown labels — match the audition-page (prototypes/fill-sounds.html) names verbatim.
export const FILL_SOUND_LABELS: Record<FillSoundId, string> = {
  softBlip: "Soft Blip", twoTone: "Two-Tone", marimba: "Marimba", cashBell: "Cash Bell",
  tick: "Tick", glassPing: "Glass Ping", pop: "Pop",
};
export const REJECT_SOUND_LABELS: Record<RejectSoundId, string> = {
  buzz: "Reject 1 — Buzz", dunDun: "Reject 2 — Dun-Dun", doubleKnock: "Reject 3 — Double Knock",
  alertBeeps: "Reject 4 — Alert Beeps", powerDown: "Reject 5 — Power-Down",
};
export const SCANNER_SOUND_LABELS: Record<ScannerSoundId, string> = {
  sonarPing: "Scan 1 — Sonar Ping", arpeggio: "Scan 2 — Arpeggio", chirp: "Scan 3 — Chirp",
  highChime: "Scan 4 — High Chime", singingBowl: "Scan 5 — Singing Bowl",
};

function optionOrOff<T extends string>(v: unknown, allowed: readonly T[], fallback: T | "off"): T | "off" {
  if (v === "off") return "off";
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
}

// Volume is clamped, not merely validated: values below 0 clamp to 0, values
// above 1 (or non-numbers) fall back to the default listening level.
function sanitizeVolume(v: unknown): number {
  if (typeof v !== "number" || Number.isNaN(v)) return DEFAULT_SOUND_CONFIG.volume;
  if (v < 0) return 0;
  if (v > 1) return DEFAULT_SOUND_CONFIG.volume;
  return v;
}

export function sanitizeSoundConfig(raw: unknown): SoundConfig {
  if (!raw || typeof raw !== "object") return { ...DEFAULT_SOUND_CONFIG };
  const r = raw as Record<string, unknown>;
  return {
    enabled: typeof r.enabled === "boolean" ? r.enabled : DEFAULT_SOUND_CONFIG.enabled,
    volume: sanitizeVolume(r.volume),
    fillSound: optionOrOff(r.fillSound, FILL_SOUND_IDS, DEFAULT_SOUND_CONFIG.fillSound),
    placeClick: typeof r.placeClick === "boolean" ? r.placeClick : DEFAULT_SOUND_CONFIG.placeClick,
    rejectSound: optionOrOff(r.rejectSound, REJECT_SOUND_IDS, DEFAULT_SOUND_CONFIG.rejectSound),
    scannerSound: optionOrOff(r.scannerSound, SCANNER_SOUND_IDS, DEFAULT_SOUND_CONFIG.scannerSound),
  };
}
