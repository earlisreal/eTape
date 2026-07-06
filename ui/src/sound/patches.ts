import type { FillSoundId, RejectSoundId, ScannerSoundId } from "./SoundConfig";

export type Variant = "buy" | "sell";
export type PatchFn = (ctx: AudioContext, out: AudioNode, variant: Variant, when: number) => void;

// ---- shared helpers: ported verbatim from prototypes/fill-sounds.html:107-140 ----

// Envelope-shaped gain node: fast attack, exponential decay to silence.
function env(c: AudioContext, when: number, peak: number, attack: number, decay: number): GainNode {
  const g = c.createGain();
  g.gain.setValueAtTime(0.0001, when);
  g.gain.exponentialRampToValueAtTime(peak, when + attack);
  g.gain.exponentialRampToValueAtTime(0.0001, when + attack + decay);
  return g;
}

function tone(
  c: AudioContext,
  type: OscillatorType,
  freq: number,
  when: number,
  peak: number,
  attack: number,
  decay: number,
  dest: AudioNode
): void {
  const o = c.createOscillator();
  o.type = type;
  o.frequency.setValueAtTime(freq, when);
  const g = env(c, when, peak, attack, decay);
  o.connect(g).connect(dest);
  o.start(when);
  o.stop(when + attack + decay + 0.05);
}

function noiseBurst(
  c: AudioContext,
  when: number,
  peak: number,
  decay: number,
  filterType: BiquadFilterType,
  freq: number,
  q: number,
  dest: AudioNode
): void {
  const len = Math.ceil(c.sampleRate * (decay + 0.02));
  const buf = c.createBuffer(1, len, c.sampleRate);
  const d = buf.getChannelData(0);
  for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
  const src = c.createBufferSource();
  src.buffer = buf;
  const f = c.createBiquadFilter();
  f.type = filterType;
  f.frequency.value = freq;
  f.Q.value = q;
  const g = env(c, when, peak, 0.002, decay);
  src.connect(f).connect(g).connect(dest);
  src.start(when);
}

// ---- fill patches (ported from lines 145-224) ----

const softBlip: PatchFn = (c, out, v, t) => {
  const f = v === "buy" ? 659.25 : 440; // E5 / A4
  const lp = c.createBiquadFilter();
  lp.type = "lowpass";
  lp.frequency.value = 2800;
  lp.connect(out);
  tone(c, "triangle", f, t, 0.5, 0.003, 0.14, lp);
};

const twoTone: PatchFn = (c, out, v, t) => {
  const seq = v === "buy" ? [523.25, 783.99] : [783.99, 523.25];
  for (let i = 0; i < 2; i++) {
    const at = t + i * 0.095;
    tone(c, "sine", seq[i], at, 0.45, 0.004, 0.09, out);
    tone(c, "sine", seq[i] * 2, at, 0.08, 0.004, 0.06, out); // body
  }
};

const marimba: PatchFn = (c, out, v, t) => {
  const f = v === "buy" ? 698.46 : 466.16; // F5 / Bb4
  tone(c, "sine", f, t, 0.5, 0.0015, 0.22, out);
  tone(c, "sine", f * 3.9, t, 0.14, 0.0015, 0.07, out); // strike partial
};

const cashBell: PatchFn = (c, out, v, t) => {
  const base = v === "buy" ? 880 : 587.33; // A5 / D5
  const partials: [number, number, number][] = [
    [1, 0.42, 0.25],
    [2.76, 0.2, 0.15],
    [5.4, 0.09, 0.08],
  ];
  for (const [ratio, peak, decay] of partials) tone(c, "sine", base * ratio, t, peak, 0.002, decay, out);
  noiseBurst(c, t, 0.12, 0.012, "highpass", 3000, 0.7, out);
};

const tick: PatchFn = (c, out, v, t) => {
  const f = v === "buy" ? 2200 : 1100;
  noiseBurst(c, t, 0.45, 0.025, "bandpass", f, 6, out);
  tone(c, "sine", f, t, 0.18, 0.002, 0.03, out);
};

const glassPing: PatchFn = (c, out, v, t) => {
  const f = v === "buy" ? 1174.66 : 783.99; // D6 / G5
  tone(c, "sine", f, t, 0.3, 0.004, 0.32, out);
  tone(c, "sine", f + 2.5, t, 0.18, 0.004, 0.32, out); // beat shimmer
  tone(c, "sine", f * 2.32, t, 0.08, 0.004, 0.12, out); // glass partial
};

const pop: PatchFn = (c, out, v, t) => {
  const [f0, f1] = v === "buy" ? [380, 720] : [720, 380];
  const o = c.createOscillator();
  o.type = "sine";
  o.frequency.setValueAtTime(f0, t);
  o.frequency.exponentialRampToValueAtTime(f1, t + 0.09);
  const lp = c.createBiquadFilter();
  lp.type = "lowpass";
  lp.frequency.value = 2000;
  const g = env(c, t, 0.55, 0.003, 0.125);
  o.connect(lp).connect(g).connect(out);
  o.start(t);
  o.stop(t + 0.2);
};

// ---- placement click (ported from EVENT_SOUNDS[0], lines 230-238) ----

export const PLACE_CLICK: PatchFn = (c, out, v, t) => {
  const f = v === "buy" ? 1800 : 900;
  noiseBurst(c, t, 0.35, 0.02, "bandpass", f, 5, out);
  tone(c, "sine", f, t, 0.12, 0.002, 0.02, out);
};

// ---- reject patches (ported from lines 240-309); ignore variant ----

const buzz: PatchFn = (c, out, _v, t) => {
  for (const f0 of [220, 226]) {
    const o = c.createOscillator();
    o.type = "sawtooth";
    o.frequency.setValueAtTime(f0, t);
    o.frequency.exponentialRampToValueAtTime(f0 * 0.77, t + 0.15);
    const lp = c.createBiquadFilter();
    lp.type = "lowpass";
    lp.frequency.value = 900;
    const g = env(c, t, 0.22, 0.005, 0.2);
    o.connect(lp).connect(g).connect(out);
    o.start(t);
    o.stop(t + 0.3);
  }
};

const dunDun: PatchFn = (c, out, _v, t) => {
  tone(c, "triangle", 466.16, t, 0.4, 0.004, 0.09, out); // Bb4
  tone(c, "triangle", 329.63, t + 0.11, 0.4, 0.004, 0.16, out); // E4
};

const doubleKnock: PatchFn = (c, out, _v, t) => {
  for (const at of [t, t + 0.12]) {
    const o = c.createOscillator();
    o.type = "sine";
    o.frequency.setValueAtTime(160, at);
    o.frequency.exponentialRampToValueAtTime(80, at + 0.05);
    const g = env(c, at, 0.55, 0.003, 0.07);
    o.connect(g).connect(out);
    o.start(at);
    o.stop(at + 0.15);
  }
};

const alertBeeps: PatchFn = (c, out, _v, t) => {
  const lp = c.createBiquadFilter();
  lp.type = "lowpass";
  lp.frequency.value = 2500;
  lp.connect(out);
  tone(c, "square", 987.77, t, 0.16, 0.003, 0.045, lp); // B5
  tone(c, "square", 987.77, t + 0.075, 0.16, 0.003, 0.045, lp);
};

const powerDown: PatchFn = (c, out, _v, t) => {
  const o = c.createOscillator();
  o.type = "sawtooth";
  o.frequency.setValueAtTime(330, t);
  o.frequency.exponentialRampToValueAtTime(110, t + 0.25);
  const lp = c.createBiquadFilter();
  lp.type = "lowpass";
  lp.frequency.value = 1200;
  const g = env(c, t, 0.3, 0.005, 0.27);
  o.connect(lp).connect(g).connect(out);
  o.start(t);
  o.stop(t + 0.35);
};

// ---- scanner patches (ported from lines 316-373); ignore variant ----

const sonarPing: PatchFn = (c, out, _v, t) => {
  const o = c.createOscillator();
  o.type = "sine";
  o.frequency.setValueAtTime(980, t);
  o.frequency.exponentialRampToValueAtTime(920, t + 0.35);
  const g = env(c, t, 0.35, 0.006, 0.42);
  o.connect(g).connect(out);
  o.start(t);
  o.stop(t + 0.5);
  tone(c, "sine", 940, t + 0.22, 0.08, 0.01, 0.28, out); // echo
};

const arpeggio: PatchFn = (c, out, _v, t) => {
  const notes = [523.25, 659.25, 783.99]; // C5 E5 G5
  notes.forEach((f, i) => {
    const at = t + i * 0.065;
    tone(c, "sine", f, at, 0.4, 0.0015, i === 2 ? 0.22 : 0.13, out);
    tone(c, "sine", f * 3.9, at, 0.1, 0.0015, 0.05, out);
  });
};

const chirp: PatchFn = (c, out, _v, t) => {
  const o = c.createOscillator();
  o.type = "triangle";
  o.frequency.setValueAtTime(600, t);
  o.frequency.exponentialRampToValueAtTime(1400, t + 0.09);
  const lp = c.createBiquadFilter();
  lp.type = "lowpass";
  lp.frequency.value = 3000;
  const g = env(c, t, 0.4, 0.004, 0.11);
  o.connect(lp).connect(g).connect(out);
  o.start(t);
  o.stop(t + 0.18);
};

const highChime: PatchFn = (c, out, _v, t) => {
  tone(c, "sine", 1318.51, t, 0.26, 0.004, 0.3, out);
  tone(c, "sine", 1321.5, t, 0.14, 0.004, 0.3, out); // shimmer beat
  tone(c, "sine", 1318.51 * 2.7, t, 0.05, 0.004, 0.1, out);
};

const singingBowl: PatchFn = (c, out, _v, t) => {
  tone(c, "sine", 330, t, 0.4, 0.015, 0.6, out);
  tone(c, "sine", 330 * 2.62, t, 0.1, 0.015, 0.35, out);
  tone(c, "sine", 330 * 4.4, t, 0.05, 0.015, 0.15, out);
};

export const FILL_PATCHES: Record<FillSoundId, PatchFn> = { softBlip, twoTone, marimba, cashBell, tick, glassPing, pop };
export const REJECT_PATCHES: Record<RejectSoundId, PatchFn> = { buzz, dunDun, doubleKnock, alertBeeps, powerDown };
export const SCANNER_PATCHES: Record<ScannerSoundId, PatchFn> = { sonarPing, arpeggio, chirp, highChime, singingBowl };

export function resolvePatch(kind: "fill" | "place" | "reject" | "scanner", id: string): PatchFn | undefined {
  if (kind === "place") return PLACE_CLICK;
  if (kind === "fill") return FILL_PATCHES[id as FillSoundId];
  if (kind === "reject") return REJECT_PATCHES[id as RejectSoundId];
  return SCANNER_PATCHES[id as ScannerSoundId];
}

// ---- real player: owns the AudioContext lifecycle ----

export interface PatchPlayer {
  unlock(): void; // lazily create + resume the AudioContext (call from a user gesture)
  setMasterVolume(v: number): void; // master gain = v*v (perceptual taper)
  play(fn: PatchFn, variant: Variant): void; // schedule immediately; drop if context isn't running
}

export class WebAudioPatchPlayer implements PatchPlayer {
  private ctx: AudioContext | null = null;
  private master: GainNode | null = null;
  private volume = 0.6;

  unlock(): void {
    const webkitCtor = (globalThis as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    const Ctor = typeof AudioContext !== "undefined" ? AudioContext : typeof webkitCtor !== "undefined" ? webkitCtor : null;
    if (!Ctor) return; // no Web Audio (SSR / node test env) -> no-op
    if (!this.ctx) {
      this.ctx = new Ctor();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.volume * this.volume;
      this.master.connect(this.ctx.destination);
    }
    if (this.ctx.state === "suspended") void this.ctx.resume();
  }

  setMasterVolume(v: number): void {
    this.volume = v;
    if (this.master) this.master.gain.value = v * v;
  }

  play(fn: PatchFn, variant: Variant): void {
    if (!this.ctx || !this.master || this.ctx.state !== "running") return; // drop, never queue
    fn(this.ctx, this.master, variant, this.ctx.currentTime);
  }
}
