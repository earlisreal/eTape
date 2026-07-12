import { describe, expect, it } from "vitest";
import { planDemoEntry, planDemoRevert } from "./demoTransition";
import type { Workspace } from "./workspace";

const UNI = ["US.AAA", "US.BBB", "US.CCC", "US.DDD", "US.EEE", "US.FFF"];
const isSymbolBearing = (panelId: string) => panelId === "chart" || panelId === "tape";

function ws(panels: Workspace["panels"], groups?: Workspace["groups"]): Workspace {
  return groups === undefined ? { name: "test", panels, layout: null } : { name: "test", panels, layout: null, groups };
}

describe("planDemoEntry", () => {
  it("rewrites all four fixed group focus entries over the sorted universe", () => {
    const next = planDemoEntry(ws([]), UNI, isSymbolBearing);
    expect(next.groups).toEqual({ green: "US.AAA", red: "US.BBB", blue: "US.CCC", yellow: "US.DDD" });
  });

  it("cycles uni[4:] across pinned symbol-bearing panels in id order", () => {
    const next = planDemoEntry(
      ws([
        { id: "chart-2", panelId: "chart", group: null, settings: { symbol: "US.OLD" } },
        { id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.OLD" } },
      ]),
      UNI,
      isSymbolBearing,
    );
    const byId = Object.fromEntries(next.panels.map((p) => [p.id, p.settings.symbol]));
    expect(byId["chart-1"]).toBe("US.EEE"); // uni[4], id-sorted first
    expect(byId["chart-2"]).toBe("US.FFF"); // uni[5]
  });

  it("wraps when more pinned panels than remaining universe", () => {
    const panels = ["a", "b", "c"].map((s) => ({ id: `chart-${s}`, panelId: "chart", group: null, settings: { symbol: "US.OLD" } }));
    const next = planDemoEntry(ws(panels), ["US.AAA", "US.BBB", "US.CCC", "US.DDD", "US.EEE"], isSymbolBearing); // only uni[4:]=[EEE]
    const syms = next.panels.map((p) => p.settings.symbol);
    expect(syms).toEqual(["US.EEE", "US.EEE", "US.EEE"]); // all wrap to the single remaining
  });

  it("leaves a grouped panel's own settings.symbol untouched but sets its group focus", () => {
    // A panel following a link group (group !== null) resolves via groups, not
    // settings.symbol, so planDemoEntry must skip it in the pinned-panel cycle
    // and only rewrite groups deterministically.
    const next = planDemoEntry(ws([{ id: "tape-1", panelId: "tape", group: "green", settings: { symbol: "US.OLD" } }]), UNI, isSymbolBearing);
    expect(next.groups?.green).toBe("US.AAA");
    expect(next.panels[0].settings.symbol).toBe("US.OLD"); // untouched — grouped, not pinned
  });

  it("skips panels whose panelId is not symbol-bearing", () => {
    const next = planDemoEntry(
      ws([{ id: "scanner-1", panelId: "scanner", group: null, settings: {} }]),
      UNI,
      isSymbolBearing,
    );
    expect(next.panels[0].settings.symbol).toBeUndefined();
  });
});

describe("planDemoRevert", () => {
  const snapshot = ws([{ id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.NVDA" } }], { green: "US.NVDA" });

  it("with snapshot returns it verbatim", () => {
    const out = planDemoRevert({ snapshot, universe: UNI }, ws([]));
    expect(out).toEqual(snapshot);
  });

  it("without snapshot patches universe symbols + group entries to the default seed", () => {
    const current = ws(
      [
        { id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.AAA" } }, // ∈ universe
        { id: "chart-2", panelId: "chart", group: null, settings: { symbol: "US.REAL" } }, // not in universe
      ],
      { green: "US.BBB", red: "US.KEEP" },
    );
    const out = planDemoRevert({ snapshot: null, universe: UNI }, current);
    expect(out.panels[0].settings.symbol).toBe("US.AAPL");
    expect(out.panels[1].settings.symbol).toBe("US.REAL");
    expect(out.groups?.green).toBe("US.AAPL");
    expect(out.groups?.red).toBe("US.KEEP");
  });
});
