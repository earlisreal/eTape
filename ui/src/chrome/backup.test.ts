import { describe, it, expect } from "vitest";
import {
  SETTINGS_EXPORT_VERSION, buildExport, parseImport,
  prepareImportedWorkspace, prepareImportedOrderConfig, detectHotkeyConflicts,
} from "./backup";
import type { Workspace } from "./workspace";
import type { ActionTemplate, OrderConfig } from "./exec/actionTemplate";

function makeWorkspace(name: string): Workspace {
  return {
    name,
    panels: [{ id: "p1", panelId: "chart", group: "red", settings: { symbol: "AAPL" } }],
    layout: { panels: { p1: {} } },
    groups: { red: "AAPL" },
    linkVenues: { red: "alpaca" },
  };
}

function makeTemplate(id: string, hotkey?: string): ActionTemplate {
  return {
    kind: "place", id, label: "Buy 100", side: "BUY", type: "LIMIT", tif: "DAY",
    session: "AUTO", priceSource: "Ask", priceOffset: 0, priceOffsetUnit: "$",
    sizing: { mode: "Shares", shares: 100 }, ...(hotkey !== undefined ? { hotkey } : {}),
  };
}

function makeOrderConfig(templates: ActionTemplate[], activeVenue = "alpaca"): OrderConfig {
  return { templates, activeVenue };
}

describe("backup: buildExport / parseImport round-trip", () => {
  it("round-trips through JSON.stringify + parseImport", () => {
    const workspace = makeWorkspace("main");
    const orderConfig = makeOrderConfig([makeTemplate("t1", "Ctrl+1")]);
    const built = buildExport({ layout: true, hotkeys: true }, { workspace, orderConfig });
    const text = JSON.stringify(built);
    const result = parseImport(text);
    expect(result.ok).toBe(true);
    if (result.ok) expect(result.data).toEqual(built);
  });
});

describe("backup: buildExport selection matrix", () => {
  const workspace = makeWorkspace("main");
  const orderConfig = makeOrderConfig([makeTemplate("t1", "Ctrl+1")]);

  it("layout only: has layout, no hotkeys", () => {
    const out = buildExport({ layout: true, hotkeys: false }, { workspace, orderConfig });
    expect(out.layout).toEqual(workspace);
    expect(out.hotkeys).toBeUndefined();
  });

  it("hotkeys only: has hotkeys, no layout", () => {
    const out = buildExport({ layout: false, hotkeys: true }, { workspace, orderConfig });
    expect(out.hotkeys).toEqual({ templates: orderConfig.templates });
    expect(out.layout).toBeUndefined();
  });

  it("both: has layout and hotkeys", () => {
    const out = buildExport({ layout: true, hotkeys: true }, { workspace, orderConfig });
    expect(out.layout).toEqual(workspace);
    expect(out.hotkeys).toEqual({ templates: orderConfig.templates });
  });

  it("neither: has no layout and no hotkeys", () => {
    const out = buildExport({ layout: false, hotkeys: false }, { workspace, orderConfig });
    expect(out.layout).toBeUndefined();
    expect(out.hotkeys).toBeUndefined();
  });

  it("always sets the envelope fields", () => {
    const out = buildExport({ layout: false, hotkeys: false }, { workspace, orderConfig });
    expect(out.app).toBe("eTape");
    expect(out.kind).toBe("settings-export");
    expect(out.version).toBe(SETTINGS_EXPORT_VERSION);
    expect(typeof out.exportedAt).toBe("string");
    expect(new Date(out.exportedAt).toISOString()).toBe(out.exportedAt);
  });
});

describe("backup: activeVenue never leaks into an export", () => {
  it("hotkeys output never includes activeVenue, even when set", () => {
    const workspace = makeWorkspace("main");
    const orderConfig = makeOrderConfig([makeTemplate("t1", "Ctrl+1")], "alpaca-live");
    const out = buildExport({ layout: false, hotkeys: true }, { workspace, orderConfig });
    expect(out.hotkeys).toEqual({ templates: orderConfig.templates });
    expect(out.hotkeys).not.toHaveProperty("activeVenue");
    expect(JSON.stringify(out)).not.toContain("activeVenue");
  });
});

describe("backup: prepareImportedOrderConfig", () => {
  it("keeps current.activeVenue verbatim and regenerates every template id", () => {
    const imported = { templates: [makeTemplate("old-1", "Ctrl+1"), makeTemplate("old-2", "Ctrl+2")] };
    const current = makeOrderConfig([], "tradezero");
    const result = prepareImportedOrderConfig(imported, current);
    expect(result.activeVenue).toBe("tradezero");
    expect(result.templates).toHaveLength(2);
    result.templates.forEach((t, i) => {
      expect(t.id).not.toBe(imported.templates[i].id);
      expect(typeof t.id).toBe("string");
      expect(t.id.length).toBeGreaterThan(0);
    });
  });

  it("produces distinct ids across templates (no accidental collision)", () => {
    const imported = { templates: [makeTemplate("old-1"), makeTemplate("old-2"), makeTemplate("old-3")] };
    const current = makeOrderConfig([]);
    const result = prepareImportedOrderConfig(imported, current);
    const ids = result.templates.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("passes the result through normalizeOrderConfig (legacy fraction migrates to pct)", () => {
    const legacy: ActionTemplate = {
      kind: "place", id: "legacy-1", label: "Half size", side: "BUY", type: "MARKET", tif: "DAY",
      priceSource: "Last", priceOffset: 0,
      sizing: { mode: "PositionFraction", fraction: "half" },
    };
    const imported = { templates: [legacy] };
    const current = makeOrderConfig([]);
    const result = prepareImportedOrderConfig(imported, current);
    const sizing = result.templates[0].kind === "place" ? result.templates[0].sizing : null;
    expect(sizing).toMatchObject({ mode: "PositionFraction", pct: 50 });
  });
});

describe("backup: prepareImportedWorkspace", () => {
  it("overwrites name with currentName regardless of the imported doc's own name", () => {
    const imported = makeWorkspace("someone-elses-workspace");
    const result = prepareImportedWorkspace(imported, "main");
    expect(result.name).toBe("main");
    expect(result.panels).toEqual(imported.panels);
    expect(result.layout).toEqual(imported.layout);
    expect(result.groups).toEqual(imported.groups);
    expect(result.linkVenues).toEqual(imported.linkVenues);
  });
});

describe("backup: parseImport rejects without throwing", () => {
  it("rejects non-JSON text", () => {
    const result = parseImport("not json at all {{{");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(typeof result.error).toBe("string");
  });

  it("rejects valid JSON missing kind", () => {
    const result = parseImport(JSON.stringify({ app: "eTape", version: 1, exportedAt: "2026-01-01T00:00:00.000Z" }));
    expect(result.ok).toBe(false);
  });

  it("rejects valid JSON with kind set to something else", () => {
    const result = parseImport(JSON.stringify({ app: "eTape", kind: "workspace-export", version: 1 }));
    expect(result.ok).toBe(false);
  });

  it("rejects valid JSON with app !== eTape", () => {
    const result = parseImport(JSON.stringify({ app: "someOtherApp", kind: "settings-export", version: 1 }));
    expect(result.ok).toBe(false);
  });

  it("never throws even for structurally odd JSON (null, array, number)", () => {
    expect(() => parseImport("null")).not.toThrow();
    expect(() => parseImport("[1,2,3]")).not.toThrow();
    expect(() => parseImport("42")).not.toThrow();
    expect(parseImport("null").ok).toBe(false);
    expect(parseImport("[1,2,3]").ok).toBe(false);
    expect(parseImport("42").ok).toBe(false);
  });
});

describe("backup: detectHotkeyConflicts", () => {
  it("returns [] when no template has a hotkey", () => {
    expect(detectHotkeyConflicts([makeTemplate("t1"), makeTemplate("t2")])).toEqual([]);
  });

  it("returns [] when all hotkeys are unique", () => {
    expect(detectHotkeyConflicts([makeTemplate("t1", "Ctrl+1"), makeTemplate("t2", "Ctrl+2")])).toEqual([]);
  });

  it("returns the shared combo when two templates share a hotkey", () => {
    const result = detectHotkeyConflicts([makeTemplate("t1", "Ctrl+1"), makeTemplate("t2", "Ctrl+1")]);
    expect(result).toEqual(["Ctrl+1"]);
  });

  it("returns each shared combo once, for three or more templates sharing across two combos", () => {
    const result = detectHotkeyConflicts([
      makeTemplate("t1", "Ctrl+1"), makeTemplate("t2", "Ctrl+1"), makeTemplate("t3", "Ctrl+1"),
      makeTemplate("t4", "Ctrl+2"), makeTemplate("t5", "Ctrl+2"),
    ]);
    expect(result.sort()).toEqual(["Ctrl+1", "Ctrl+2"]);
  });
});
