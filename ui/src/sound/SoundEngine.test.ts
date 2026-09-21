import { describe, it, expect, vi, beforeEach } from "vitest";
import { SoundEngine } from "./SoundEngine";
import type { PatchPlayer, PatchFn, Variant } from "./patches";
import { DEFAULT_SOUND_CONFIG } from "./SoundConfig";

interface Played { fn: PatchFn; variant: Variant }
function fakePlayer() {
  const played: Played[] = [];
  const player: PatchPlayer & { played: Played[] } = {
    played,
    unlock: vi.fn(),
    setMasterVolume: vi.fn(),
    play: (fn, variant) => { played.push({ fn, variant }); },
    preloadRecording: vi.fn(async () => {}),
    playRecording: vi.fn(() => true),
  };
  return player;
}
function make(now: () => number) {
  const player = fakePlayer();
  const eng = new SoundEngine(player, now);
  eng.setConfig({ ...DEFAULT_SOUND_CONFIG });
  return { eng, player };
}

describe("SoundEngine", () => {
  let t = 0;
  const now = () => t;
  beforeEach(() => { t = 100_000; });

  it("orderFilled plays; buy and sell are separate channels (never mask)", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t);
    eng.orderFilled("SELL", t);   // same instant, different channel -> both play
    expect(player.played).toHaveLength(2);
    expect(player.played[0].variant).toBe("buy");
    expect(player.played[1].variant).toBe("sell");
  });

  it("coalesces the same channel within 200ms and plays again after", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t);            // plays
    t += 150; eng.orderFilled("BUY", t);  // suppressed (<200)
    t += 100; eng.orderFilled("BUY", t);  // 250ms since last play -> plays
    expect(player.played).toHaveLength(2);
  });

  it("coalesces Scanner calls within 200ms and plays again after", () => {
    const { eng, player } = make(now);
    eng.scannerHit();
    t += 150; eng.scannerHit();
    t += 100; eng.scannerHit();
    expect(player.played).toHaveLength(2);
  });

  it("freshness guard: a fill older than 10s is silent, a fresh one chimes", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t - 10_001);   // stale
    eng.orderFilled("SELL", t - 5_000);   // fresh (within 10s)
    expect(player.played).toHaveLength(1);
    expect(player.played[0].variant).toBe("sell");
  });

  it("config gating: master off silences everything; per-event off silences that channel", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, enabled: false });
    eng.orderFilled("BUY", t); eng.orderRejected(); eng.scannerHit(); eng.orderPlaced("BUY");
    expect(player.played).toHaveLength(0);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, fillSound: "off", placeClick: false });
    t += 1000; eng.orderFilled("BUY", t);   // fill off
    eng.orderPlaced("BUY");                  // placeClick off
    eng.orderRejected();                     // still on -> plays
    expect(player.played).toHaveLength(1);
  });

  it("orderPlaced maps SELL/SHORT to the sell variant, BUY/COVER to buy", () => {
    const { eng, player } = make(now);
    eng.orderPlaced("SELL");
    t += 300; eng.orderPlaced("BUY");
    expect(player.played.map((p) => p.variant)).toEqual(["sell", "buy"]);
  });

  it("double-fire absorption: ack-reject + stream-reject within 200ms play once", () => {
    const { eng, player } = make(now);
    eng.orderRejected();          // ack path (blocked)
    t += 50; eng.orderRejected(); // stream path (REJECTED) shortly after
    expect(player.played).toHaveLength(1);
  });

  it("preview bypasses gating but not availability", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, enabled: false, scannerSound: "off" });
    eng.preview("scanner", "arpeggio");   // gating bypassed
    expect(player.played).toHaveLength(1);
  });

  it("previews both selected session recordings and respects its toggle", () => {
    vi.useFakeTimers();
    try {
      const { eng, player } = make(now);
      eng.setConfig({ ...DEFAULT_SOUND_CONFIG, sessionVoiceEnabled: false });
      eng.previewSessionVoice();
      expect(player.playRecording).not.toHaveBeenCalled();

      eng.setConfig({ ...DEFAULT_SOUND_CONFIG, sessionVoice: "zira" });
      eng.previewSessionVoice();
      expect(player.playRecording).toHaveBeenNthCalledWith(1, "/audio/premarket-open.mp3");
      vi.advanceTimersByTime(1_750);
      expect(player.playRecording).toHaveBeenNthCalledWith(2, "/audio/market-open.mp3");
    } finally {
      vi.useRealTimers();
    }
  });

  it("setConfig pushes volume into the player", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, volume: 0.4 });
    expect(player.setMasterVolume).toHaveBeenLastCalledWith(0.4);
  });

  it("preloads both voices on unlock and obeys voice, mute, and volume", () => {
    const { eng, player } = make(now);
    eng.unlock();
    expect(player.unlock).toHaveBeenCalledOnce();
    expect(player.preloadRecording).toHaveBeenCalledWith("/audio/premarket-open-google.mp3");
    expect(player.preloadRecording).toHaveBeenCalledWith("/audio/market-open-google.mp3");
    expect(player.preloadRecording).toHaveBeenCalledWith("/audio/premarket-open.mp3");
    expect(player.preloadRecording).toHaveBeenCalledWith("/audio/market-open.mp3");
    expect(eng.sessionOpened("pre")).toBe(true);
    expect(eng.sessionOpened("rth")).toBe(true);
    expect(player.playRecording).toHaveBeenNthCalledWith(1, "/audio/premarket-open-google.mp3");
    expect(player.playRecording).toHaveBeenNthCalledWith(2, "/audio/market-open-google.mp3");
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, sessionVoice: "zira" });
    expect(eng.sessionOpened("pre")).toBe(true);
    expect(eng.sessionOpened("rth")).toBe(true);
    expect(player.playRecording).toHaveBeenNthCalledWith(3, "/audio/premarket-open.mp3");
    expect(player.playRecording).toHaveBeenNthCalledWith(4, "/audio/market-open.mp3");
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG });
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, enabled: false });
    expect(eng.sessionOpened("pre")).toBe(false);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, sessionVoiceEnabled: false });
    expect(eng.sessionOpened("rth")).toBe(false);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, volume: 0 });
    expect(eng.sessionOpened("rth")).toBe(false);
    expect(player.playRecording).toHaveBeenCalledTimes(4);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG });
    vi.mocked(player.playRecording).mockReturnValue(false);
    expect(eng.sessionOpened("pre")).toBe(false);
  });
});
