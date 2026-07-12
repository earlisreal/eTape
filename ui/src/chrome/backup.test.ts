import { describe, it, expect } from "vitest";
import {
  SETTINGS_EXPORT_VERSION, buildExport, parseImport,
  prepareImportedWorkspace, prepareImportedOrderConfig, detectHotkeyConflicts, isPresentLayout,
  collectPanelIds, reconcileToGrid,
} from "./backup";
import type { Workspace } from "./workspace";
import type { ActionTemplate, OrderConfig } from "./exec/actionTemplate";

function makeWorkspace(name: string): Workspace {
  return {
    name,
    panels: [{ id: "p1", panelId: "chart", group: "red", settings: { symbol: "AAPL" } }],
    layout: {
      grid: {
        root: { type: "leaf", data: { views: ["p1"], activeView: "p1", id: "1" }, size: 500 },
        width: 1000,
        height: 500,
        orientation: "HORIZONTAL",
      },
      panels: { p1: { id: "p1", contentComponent: "chart-p1", title: "Chart" } },
      activeGroup: "1",
    },
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

describe("backup: isPresentLayout", () => {
  it("returns true for a workspace object", () => {
    expect(isPresentLayout(makeWorkspace("main"))).toBe(true);
  });

  it("returns false for undefined (no layout section in the file)", () => {
    expect(isPresentLayout(undefined)).toBe(false);
  });

  it("returns false for null", () => {
    expect(isPresentLayout(null as unknown as Workspace)).toBe(false);
  });

  it("returns false for an array", () => {
    expect(isPresentLayout([] as unknown as Workspace)).toBe(false);
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

describe("backup: collectPanelIds", () => {
  it("collects panel ids from nested branches with multiple leaves", () => {
    const layout = {
      grid: {
        root: {
          type: "branch",
          data: [
            { type: "leaf", data: { views: ["chart-1"], activeView: "chart-1", id: "1" }, size: 670 },
            {
              type: "branch",
              data: [
                { type: "leaf", data: { views: ["t-dom"], activeView: "t-dom", id: "t-ticket" }, size: 294 },
                { type: "leaf", data: { views: ["t-tape"], activeView: "t-tape", id: "t-tape" }, size: 237 },
              ],
              size: 308,
            },
          ],
          size: 926,
        },
        width: 1920,
        height: 926,
        orientation: "HORIZONTAL",
      },
      panels: {},
      activeGroup: "t-tape",
    };
    const result = collectPanelIds(layout);
    expect(result).toEqual(new Set(["chart-1", "t-dom", "t-tape"]));
  });

  it("collects panel ids from a leaf with multi-panel views array (tabbed group)", () => {
    const layout = {
      grid: {
        root: {
          type: "leaf",
          data: {
            views: ["panel-a", "panel-b", "panel-c"],
            activeView: "panel-a",
            id: "group-id",
          },
          size: 100,
        },
      },
    };
    const result = collectPanelIds(layout);
    expect(result).toEqual(new Set(["panel-a", "panel-b", "panel-c"]));
  });

  it("returns empty set for null layout", () => {
    expect(collectPanelIds(null)).toEqual(new Set());
  });

  it("returns empty set for empty object layout", () => {
    expect(collectPanelIds({})).toEqual(new Set());
  });

  it("returns empty set for layout with null grid", () => {
    expect(collectPanelIds({ grid: null })).toEqual(new Set());
  });

  it("returns empty set for layout with non-object grid root", () => {
    expect(collectPanelIds({ grid: { root: "not-an-object" } })).toEqual(new Set());
  });

  it("does not throw on malformed/truncated layout", () => {
    expect(() => collectPanelIds({ grid: { root: { type: "branch", data: null } } })).not.toThrow();
    expect(() => collectPanelIds({ grid: { root: { type: "leaf", data: { views: "not-array" } } } })).not.toThrow();
    expect(() => collectPanelIds({ grid: { root: { type: "unknown", data: [] } } })).not.toThrow();
  });
});

describe("backup: reconcileToGrid", () => {
  it("filters panels to only those referenced by the grid", () => {
    const panelA = { id: "A", panelId: "chart", group: "red" as const, settings: { symbol: "AAPL" } };
    const panelB = { id: "B", panelId: "tape", group: null, settings: { columns: 5 } };
    const panelC = { id: "C", panelId: "dom", group: "blue" as const, settings: { levels: 10 } };

    const base: Workspace = {
      name: "test",
      panels: [panelA, panelB, panelC],
      layout: {
        grid: {
          root: {
            type: "branch",
            data: [
              { type: "leaf", data: { views: ["A"], activeView: "A", id: "1" }, size: 500 },
              { type: "leaf", data: { views: ["C"], activeView: "C", id: "2" }, size: 500 },
            ],
            size: 1000,
          },
        },
      },
    };

    const result = reconcileToGrid(base, base.layout);

    expect(result.panels).toHaveLength(2);
    expect(result.panels[0]).toBe(panelA);
    expect(result.panels[1]).toBe(panelC);
    expect(result.layout).toBe(base.layout);
    expect(result.name).toBe("test");
  });

  it("preserves original order and identity of kept entries", () => {
    const panels = [
      { id: "id-1", panelId: "chart", group: "red" as const, settings: { a: 1 } },
      { id: "id-2", panelId: "tape", group: null, settings: { b: 2 } },
      { id: "id-3", panelId: "dom", group: "blue" as const, settings: { c: 3 } },
    ];

    const base: Workspace = {
      name: "test",
      panels,
      layout: {
        grid: {
          root: {
            type: "branch",
            data: [
              { type: "leaf", data: { views: ["id-1", "id-3"], activeView: "id-1", id: "g1" }, size: 500 },
            ],
            size: 500,
          },
        },
      },
    };

    const result = reconcileToGrid(base, base.layout);

    expect(result.panels).toHaveLength(2);
    expect(result.panels[0]).toBe(panels[0]);
    expect(result.panels[1]).toBe(panels[2]);
  });

  it("returns base unchanged when layout is not a present layout", () => {
    const base: Workspace = {
      name: "test",
      panels: [{ id: "p1", panelId: "chart", group: "red", settings: {} }],
      layout: null,
    };

    const result = reconcileToGrid(base, null);

    expect(result).toBe(base);
    expect(result.panels).toBe(base.panels);
  });

  it("returns base unchanged when layout is an empty object", () => {
    const base: Workspace = {
      name: "test",
      panels: [{ id: "p1", panelId: "chart", group: "red", settings: {} }],
      layout: {},
    };

    const result = reconcileToGrid(base, {});

    expect(result).toEqual(base);
    expect(result.panels).toEqual(base.panels);
  });

  it("returns base unchanged when layout is not an object", () => {
    const base: Workspace = {
      name: "test",
      panels: [{ id: "p1", panelId: "chart", group: "red", settings: {} }],
      layout: "invalid",
    };

    expect(reconcileToGrid(base, "invalid")).toBe(base);
    expect(reconcileToGrid(base, 42 as unknown as Workspace)).toBe(base);
    expect(reconcileToGrid(base, [] as unknown as Workspace)).toBe(base);
  });

  it("returns base unchanged when layout has a null grid (present key, malformed value)", () => {
    const base: Workspace = {
      name: "test",
      panels: [
        { id: "p1", panelId: "chart", group: "red", settings: {} },
        { id: "p2", panelId: "tape", group: null, settings: {} },
      ],
      layout: { grid: null },
    };

    const result = reconcileToGrid(base, { grid: null });

    expect(result).toBe(base);
    expect(result.panels).toBe(base.panels);
  });

  it("returns base unchanged when grid.root is not an object", () => {
    const base: Workspace = {
      name: "test",
      panels: [
        { id: "p1", panelId: "chart", group: "red", settings: {} },
        { id: "p2", panelId: "tape", group: null, settings: {} },
      ],
      layout: { grid: { root: "garbage" } },
    };

    const result = reconcileToGrid(base, { grid: { root: "garbage" } });

    expect(result).toBe(base);
    expect(result.panels).toBe(base.panels);
  });

  it("returns base unchanged when grid has no root key at all", () => {
    const base: Workspace = {
      name: "test",
      panels: [
        { id: "p1", panelId: "chart", group: "red", settings: {} },
        { id: "p2", panelId: "tape", group: null, settings: {} },
      ],
      layout: { grid: {} },
    };

    const result = reconcileToGrid(base, { grid: {} });

    expect(result).toBe(base);
    expect(result.panels).toBe(base.panels);
  });

  it("returns base unchanged when grid.root has a branch type but malformed data", () => {
    const base: Workspace = {
      name: "test",
      panels: [
        { id: "p1", panelId: "chart", group: "red", settings: {} },
        { id: "p2", panelId: "tape", group: null, settings: {} },
      ],
      layout: { grid: { root: { type: "branch", data: null } } },
    };

    const result = reconcileToGrid(base, { grid: { root: { type: "branch", data: null } } });

    expect(result).toBe(base);
    expect(result.panels).toBe(base.panels);
  });

  it("filters panels to empty array for a well-formed grid that legitimately places zero panels", () => {
    const base: Workspace = {
      name: "test",
      panels: [
        { id: "p1", panelId: "chart", group: "red", settings: {} },
        { id: "p2", panelId: "tape", group: null, settings: {} },
      ],
      layout: { grid: { root: { type: "leaf", data: { views: [] } } } },
    };

    const result = reconcileToGrid(base, { grid: { root: { type: "leaf", data: { views: [] } } } });

    expect(result).not.toBe(base);
    expect(result.panels).toEqual([]);
  });
});

describe("backup: prepareImportedWorkspace with reconciliation", () => {
  it("heals a ghosted file with 13 panels but only 9 placed in grid", () => {
    const panels = [
      { id: "chart-1", panelId: "chart", group: "red" as const, settings: { symbol: "AAPL" } },
      { id: "chart-2", panelId: "chart", group: "red" as const, settings: { symbol: "MSFT" } },
      { id: "tape-1", panelId: "tape", group: null, settings: {} },
      { id: "dom-1", panelId: "dom", group: "blue" as const, settings: {} },
      { id: "watchlist-1", panelId: "watchlist", group: "green" as const, settings: {} },
      { id: "watchlist-2", panelId: "watchlist", group: "green" as const, settings: {} }, // ghost
      { id: "watchlist-3", panelId: "watchlist", group: "green" as const, settings: {} }, // ghost
      { id: "scanner-1", panelId: "scanner", group: "yellow" as const, settings: {} },
      { id: "scanner-2", panelId: "scanner", group: "yellow" as const, settings: {} }, // ghost
      { id: "ghost-1", panelId: "unknown", group: null, settings: {} }, // ghost
      { id: "ghost-2", panelId: "unknown", group: null, settings: {} }, // ghost
      { id: "ghost-3", panelId: "unknown", group: null, settings: {} }, // ghost
      { id: "ghost-4", panelId: "unknown", group: null, settings: {} }, // ghost
    ];

    const imported: Workspace = {
      name: "old-workspace",
      panels,
      layout: {
        grid: {
          root: {
            type: "branch",
            data: [
              { type: "leaf", data: { views: ["chart-1"], activeView: "chart-1", id: "1" }, size: 670 },
              {
                type: "branch",
                data: [
                  { type: "leaf", data: { views: ["tape-1"], activeView: "tape-1", id: "t-ticket" }, size: 294 },
                  {
                    type: "branch",
                    data: [
                      { type: "leaf", data: { views: ["dom-1"], activeView: "dom-1", id: "dom-group" }, size: 150 },
                      { type: "leaf", data: { views: ["chart-2"], activeView: "chart-2", id: "chart-2-group" }, size: 150 },
                    ],
                    size: 300,
                  },
                ],
                size: 600,
              },
            ],
            size: 1270,
          },
          width: 1920,
          height: 926,
          orientation: "HORIZONTAL",
        },
      },
      groups: { red: "AAPL", green: "DEFAULT", blue: "DEFAULT", yellow: "DEFAULT" },
      linkVenues: { red: "alpaca", green: "alpaca", blue: "alpaca", yellow: "alpaca" },
    };

    const result = prepareImportedWorkspace(imported, "current-workspace");

    // Check rename happened
    expect(result.name).toBe("current-workspace");

    // Check that only 4 panels remain (those in the grid)
    expect(result.panels).toHaveLength(4);
    const panelIds = result.panels.map((p) => p.id);
    expect(panelIds).toEqual(["chart-1", "chart-2", "tape-1", "dom-1"]);

    // Check that every grid-placed panel is present
    const gridPanelIds = collectPanelIds(result.layout);
    for (const id of gridPanelIds) {
      expect(result.panels.some((p) => p.id === id)).toBe(true);
    }

    // Check that original settings are preserved
    const chart1 = result.panels.find((p) => p.id === "chart-1");
    expect(chart1).toEqual(panels[0]);
  });

  it("keeps existing rename behavior during reconciliation", () => {
    const imported = makeWorkspace("someone-elses-workspace");
    const result = prepareImportedWorkspace(imported, "main");
    expect(result.name).toBe("main");
    expect(result.layout).toEqual(imported.layout);
    expect(result.groups).toEqual(imported.groups);
    expect(result.linkVenues).toEqual(imported.linkVenues);
  });

  it("leaves panels untouched when layout is null or missing", () => {
    const panels = [
      { id: "p1", panelId: "chart", group: "red" as const, settings: { a: 1 } },
      { id: "p2", panelId: "tape", group: null, settings: { b: 2 } },
      { id: "p3", panelId: "dom", group: "blue" as const, settings: { c: 3 } },
    ];

    const imported: Workspace = {
      name: "old",
      panels,
      layout: null,
    };

    const result = prepareImportedWorkspace(imported, "new");

    expect(result.name).toBe("new");
    expect(result.panels).toHaveLength(3);
    expect(result.panels).toEqual(panels);
    expect(result.layout).toBeNull();
  });

  it("leaves panels untouched when layout is empty object", () => {
    const panels = [
      { id: "p1", panelId: "chart", group: "red" as const, settings: { a: 1 } },
      { id: "p2", panelId: "tape", group: null, settings: { b: 2 } },
    ];

    const imported: Workspace = {
      name: "old",
      panels,
      layout: {},
    };

    const result = prepareImportedWorkspace(imported, "new");

    expect(result.name).toBe("new");
    expect(result.panels).toHaveLength(2);
    expect(result.panels).toEqual(panels);
    expect(result.layout).toEqual({});
  });
});
