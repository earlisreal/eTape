import { describe, it, expect } from "vitest";
import { DEFAULT_TEMPLATES, DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, normalizeOrderConfig, type OrderConfig, type DeckColor } from "./actionTemplate";

describe("action templates", () => {
  it("ships with zero default templates/hotkeys (blank slate; user builds their own)", () => {
    expect(DEFAULT_TEMPLATES).toEqual([]);
  });
  it("default order config wraps templates + an empty active venue", () => {
    expect(ORDER_CONFIG_KEY).toBe("orderConfig");
    expect(DEFAULT_ORDER_CONFIG.templates).toEqual(DEFAULT_TEMPLATES);
    expect(DEFAULT_ORDER_CONFIG.activeVenue).toBe("");
  });
});

describe("normalizeOrderConfig", () => {
  it("migrates fraction all/half to pct 100/50 and defaults offset unit", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [
        { kind: "place", id: "a", label: "A", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "all" } },
        { kind: "place", id: "b", label: "B", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "half" } },
      ] as OrderConfig["templates"],
    };
    const out = normalizeOrderConfig(raw);
    const a = out.templates[0];
    const b = out.templates[1];
    expect(a.kind === "place" && a.priceOffsetUnit).toBe("$");
    expect(a.kind === "place" && a.sizing.pct).toBe(100);
    expect(b.kind === "place" && b.sizing.pct).toBe(50);
  });
  it("is idempotent", () => {
    const raw: OrderConfig = {
      activeVenue: "v",
      templates: [{ kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0.1, priceOffsetUnit: "%", sizing: { mode: "PositionFraction", pct: 50 } }] as OrderConfig["templates"],
    };
    expect(normalizeOrderConfig(normalizeOrderConfig(raw))).toEqual(normalizeOrderConfig(raw));
  });
  it("passes manage templates through untouched", () => {
    const raw: OrderConfig = { activeVenue: "", templates: [{ kind: "manage", id: "k", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" }] as OrderConfig["templates"] };
    expect(normalizeOrderConfig(raw).templates[0]).toEqual(raw.templates[0]);
  });
  it("defaults a missing session to AUTO (a config saved before this feature keeps today's behavior)", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [{ kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 100 } }] as OrderConfig["templates"],
    };
    const out = normalizeOrderConfig(raw);
    expect(out.templates[0].kind === "place" && out.templates[0].session).toBe("AUTO");
  });
  it("leaves an explicit session untouched", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [{ kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", session: "OVERNIGHT", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 100 } }] as OrderConfig["templates"],
    };
    const out = normalizeOrderConfig(raw);
    expect(out.templates[0].kind === "place" && out.templates[0].session).toBe("OVERNIGHT");
  });
  it("preserves deck and deckColor fields on PlaceOrderTemplate through normalize", () => {
    const deckColors: DeckColor[] = ["auto", "green", "red", "bronze", "neutral", "danger"];
    for (const color of deckColors) {
      const raw: OrderConfig = {
        activeVenue: "",
        templates: [{ kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 100 }, deck: true, deckColor: color }] as OrderConfig["templates"],
      };
      const out = normalizeOrderConfig(raw);
      const template = out.templates[0];
      expect(template.kind === "place" && template.deck).toBe(true);
      expect(template.kind === "place" && template.deckColor).toBe(color);
    }
  });
  it("preserves deck and deckColor fields on ManagementTemplate through normalize", () => {
    const deckColors: DeckColor[] = ["auto", "green", "red", "bronze", "neutral", "danger"];
    for (const color of deckColors) {
      const raw: OrderConfig = {
        activeVenue: "",
        templates: [{ kind: "manage", id: "k", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K", deck: true, deckColor: color }] as OrderConfig["templates"],
      };
      const out = normalizeOrderConfig(raw);
      const template = out.templates[0];
      expect(template.kind === "manage" && template.deck).toBe(true);
      expect(template.kind === "manage" && template.deckColor).toBe(color);
    }
  });
  it("defaults a missing extHoursMarketBufferPct to 1.0", () => {
    const raw: OrderConfig = { activeVenue: "", templates: [] };
    expect(normalizeOrderConfig(raw).extHoursMarketBufferPct).toBe(1.0);
  });
  it("clamps extHoursMarketBufferPct to [0.1, 10]", () => {
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 0 }).extHoursMarketBufferPct).toBe(0.1);
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 50 }).extHoursMarketBufferPct).toBe(10);
  });
  it("preserves an in-range extHoursMarketBufferPct", () => {
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 2.5 }).extHoursMarketBufferPct).toBe(2.5);
  });
});
