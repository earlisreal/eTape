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
  it("removes retired conversion shortcuts and their Deck cells on load or import",()=>{
    const config=normalizeOrderConfig(JSON.parse('{"activeVenue":"sim","templates":[{"kind":"manage","id":"old-trail","label":"Convert","action":"ConvertProtectionToTrail","hotkey":"Ctrl+T","deck":true},{"kind":"manage","id":"cancel","label":"Cancel","action":"CancelLast","hotkey":"Ctrl+X"}],"hotkeyDeck":{"rows":[["old-trail","cancel"]],"showHotkeyLabels":true}}'));
    expect(config.templates.map(t=>t.id)).toEqual(["cancel"]);
    expect(config.hotkeyDeck?.rows).toEqual([["cancel"]]);
    expect(normalizeOrderConfig(config)).toEqual(config);
  });
  it("normalizes partial imported trail settings without inventing a distance or accepting market execution",()=>{
    const config=normalizeOrderConfig(JSON.parse('{"activeVenue":"sim","templates":[{"kind":"place","id":"t","label":"Trail","type":"TRAILING_STOP","side":"SELL","tif":"DAY","priceSource":"Bid","priceOffset":0,"sizing":{"mode":"PositionFraction","pct":100},"trail":{"mode":"PRICE","pricing":"MARKET"}}]}'));
    const t=config.templates[0];
    expect(t.kind==="place" && t.trail).toMatchObject({mode:"PRICE",value:0,pricing:"MARKET",sessions:{pre:true,rth:true,post:true},cushion:{value:0,unit:"$"},buffer:{value:0,unit:"$"},offset:{value:0,unit:"$"}});
  });
  it("retains trailing protection through config export and import, while legacy presets stay Fixed",()=>{
    const config:OrderConfig={activeVenue:"sim",templates:[{kind:"risk",id:"r",label:"Risk",mode:"Dollar",value:100,buyCushion:{value:0,unit:"$"},sellCushion:{value:.02,unit:"$"},
      protection:{mode:"CANDLE",value:0,timeframe:"5m",sessions:{pre:true,rth:true,post:true},buffer:{value:.1,unit:"%"},pricing:"CUSHION",cushion:{value:.02,unit:"$"},offset:{value:0,unit:"$"}}}]};
    expect(normalizeOrderConfig(JSON.parse(JSON.stringify(config))).templates[0]).toMatchObject(config.templates[0]);
    const legacy={...config.templates[0]};if(legacy.kind==="risk")delete legacy.protection;
    expect(normalizeOrderConfig({...config,templates:[legacy]}).templates[0]).not.toHaveProperty("protection");
  });
	 it("persists risk presets and defaults auto-send off", () => {
	   const raw: OrderConfig = { activeVenue: "v", templates: [{kind:"risk",id:"r",label:"Risk",mode:"CashPct",value:1,hotkey:"Ctrl+R",buyCushion:{value:0.05,unit:"$"},sellCushion:{value:0.1,unit:"%"}}] };
	   expect(normalizeOrderConfig(raw).chartRiskAutoSend).toBe(false);
	   expect(normalizeOrderConfig(raw).templates[0]).toMatchObject(raw.templates[0]);
	   expect(normalizeOrderConfig({...raw,chartRiskAutoSend:true}).chartRiskAutoSend).toBe(true);
	 });
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
    expect(normalizeOrderConfig(raw).templates[0]).toMatchObject(raw.templates[0]);
    expect(normalizeOrderConfig(raw).templates[0].deck).toBe(false);
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
  it("passes CashPct sizing through normalization unchanged", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [{ kind: "place", id: "cash", label: "Cash", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "CashPct", pct: 50 } }] as OrderConfig["templates"],
    };
    const out = normalizeOrderConfig(raw).templates[0];
    expect(out.kind === "place" && out.sizing).toEqual({ mode: "CashPct", pct: 50 });
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
  it("defaults STOP_LIMIT cushion to zero dollars and removes it from other types", () => {
    const out = normalizeOrderConfig({
      activeVenue: "",
      templates: [
        { kind: "place", id: "sl", label: "SL", side: "BUY", type: "STOP_LIMIT", tif: "DAY", priceSource: "Last", priceOffset: 0, sizing: { mode: "Shares", shares: 1 } },
        { kind: "place", id: "lmt", label: "LMT", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Last", priceOffset: 0, sizing: { mode: "Shares", shares: 1 }, limitCushion: 1, chartBinding: "Shift" },
      ] as OrderConfig["templates"],
    });
    expect(out.templates[0]).toMatchObject({ limitCushion: 0, limitCushionUnit: "$" });
    expect(out.templates[1]).not.toHaveProperty("chartBinding");
    expect(out.templates[1]).not.toHaveProperty("limitCushion");
  });
  it("keeps the first unique STOP_LIMIT chart binding and clears invalid or duplicate bindings", () => {
    const out = normalizeOrderConfig({
      activeVenue: "",
      templates: ["a", "b", "c", "d"].map((id, i) => ({
        kind: "place", id, label: id, side: "BUY", type: "STOP_LIMIT", tif: "DAY", priceSource: "Last", priceOffset: 0,
        sizing: { mode: "Shares", shares: 1 }, chartBinding: ["Shift", "Shift", "Meta", "Ctrl+Shift"][i],
      })) as OrderConfig["templates"],
    });
    expect(out.templates.map((t) => t.kind === "place" ? t.chartBinding : undefined)).toEqual(["Shift", undefined, undefined, "Ctrl+Shift"]);
  });
  it("migrates legacy deck flags into one row and defaults labels off", () => {
    const out = normalizeOrderConfig({
      activeVenue: "",
      templates: [
        { kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 1 }, deck: true },
        { kind: "place", id: "b", label: "B", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "Shares", shares: 1 } },
        { kind: "manage", id: "c", label: "C", action: "CancelLast", deck: true },
      ] as OrderConfig["templates"],
    });
    expect(out.hotkeyDeck).toEqual({ rows: [["a", "c"]], showHotkeyLabels: false });
    expect(out.templates.map((t) => t.deck)).toEqual([true, false, true]);
  });
  it("preserves valid explicit rows and labels while dropping empty, stale, and duplicate references", () => {
    const raw: OrderConfig = {
      activeVenue: "",
      templates: [
        { kind: "place", id: "a", label: "A", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 1 }, deck: true },
        { kind: "place", id: "b", label: "B", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "Shares", shares: 1 } },
      ] as OrderConfig["templates"],
      hotkeyDeck: { rows: [[], ["missing", "b", "a", "b"], ["a"]], showHotkeyLabels: true },
    };
    const out = normalizeOrderConfig(raw);
    expect(out.hotkeyDeck).toEqual({ rows: [["b", "a"]], showHotkeyLabels: true });
    expect(out.templates.map((t) => t.deck)).toEqual([true, true]);
  });
  it("does not fall back to legacy flags when an explicit layout is present but malformed", () => {
    const out = normalizeOrderConfig({
      activeVenue: "",
      templates: [{ kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", deck: true }] as OrderConfig["templates"],
      hotkeyDeck: { rows: "not rows" as unknown as string[][], showHotkeyLabels: true },
    });
    expect(out.hotkeyDeck).toEqual({ rows: [], showHotkeyLabels: true });
    expect(out.templates[0].deck).toBe(false);
  });
});
