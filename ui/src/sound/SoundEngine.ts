import type { Side } from "../wire/contract";
import { sideIsSell } from "../wire/orderStatus";
import { DEFAULT_SOUND_CONFIG, type SoundConfig } from "./SoundConfig";
import { WebAudioPatchPlayer, resolvePatch, type PatchPlayer, type Variant } from "./patches";

const COALESCE_MS = 200;
const FILL_FRESHNESS_MS = 10_000;

export interface SoundApi {
  orderPlaced(side: Side): void;
  orderRejected(): void;
}
export interface SoundSink {
  orderFilled(side: Side, tsMs: number): void;
  orderRejected(): void;
  scannerHit(): void;
  unlock(): void;
}

export class SoundEngine implements SoundApi, SoundSink {
  private cfg: SoundConfig = { ...DEFAULT_SOUND_CONFIG };
  private readonly lastPlay = new Map<string, number>();

  constructor(
    private readonly player: PatchPlayer = new WebAudioPatchPlayer(),
    private readonly now: () => number = () => Date.now(),
  ) {}

  unlock(): void { this.player.unlock(); }

  setConfig(cfg: SoundConfig): void {
    this.cfg = cfg;
    this.player.setMasterVolume(cfg.volume);
  }

  orderPlaced(side: Side): void {
    if (!this.cfg.enabled || !this.cfg.placeClick) return;
    const variant: Variant = sideIsSell(side) ? "sell" : "buy";
    this.fire(`place:${variant}`, "place", "click", variant);
  }

  orderFilled(side: Side, tsMs: number): void {
    if (!this.cfg.enabled || this.cfg.fillSound === "off") return;
    if (tsMs < this.now() - FILL_FRESHNESS_MS) return; // stale backfill stays silent
    const variant: Variant = sideIsSell(side) ? "sell" : "buy";
    this.fire(`fill:${variant}`, "fill", this.cfg.fillSound, variant);
  }

  orderRejected(): void {
    if (!this.cfg.enabled || this.cfg.rejectSound === "off") return;
    this.fire("reject", "reject", this.cfg.rejectSound, "buy");
  }

  scannerHit(): void {
    if (!this.cfg.enabled || this.cfg.scannerSound === "off") return;
    this.fire("scanner", "scanner", this.cfg.scannerSound, "buy");
  }

  preview(kind: "fill" | "place" | "reject" | "scanner", id: string): void {
    const fn = resolvePatch(kind, id);
    if (fn) this.player.play(fn, "buy"); // bypasses gating + coalescing; master volume still applies
  }

  // Per-channel 200ms coalescing gate, then delegate to the player.
  private fire(channel: string, kind: "fill" | "place" | "reject" | "scanner", id: string, variant: Variant): void {
    const t = this.now();
    const last = this.lastPlay.get(channel);
    if (last !== undefined && t - last < COALESCE_MS) return;
    this.lastPlay.set(channel, t);
    const fn = resolvePatch(kind, id);
    if (fn) this.player.play(fn, variant);
  }
}

export const soundEngine = new SoundEngine();
