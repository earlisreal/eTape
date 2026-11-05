import { describe, expect, it } from "vitest";
import { ScreenerStore } from "./ScreenerStore";
import { DEFAULT_SCREENER_FILTERS } from "../chrome/screenerFilters";
import type { ScreenerState } from "../wire/contract";

export function screenState(overrides: Partial<ScreenerState> = {}): ScreenerState {
  return { panelId: "main/screen", runId: 1, status: "complete", asOf: "2026-11-04", startedAt: "", finishedAt: "", filters: { ...DEFAULT_SCREENER_FILTERS }, universe: 100, candidates: 2, checked: 2, unavailable: 0, warning: "", error: "", rows: [], ...overrides };
}
describe("ScreenerStore", () => {
  it("isolates panels and rejects superseded run updates", () => {
    const store = new ScreenerStore();
    const apply = (state: ScreenerState) => store.apply({ kind: "delta", topic: "screener.state", payload: state });
    apply(screenState({ runId: 2 }));
    apply(screenState({ panelId: "other/screen", runId: 3 }));
    apply(screenState({ runId: 1, status: "canceled" }));
    expect(store.getSnapshot().get("main/screen")?.runId).toBe(2);
    expect(store.getSnapshot().size).toBe(2);
    apply(screenState({ runId: 2, status: "removed" }));
    expect(store.getSnapshot().has("main/screen")).toBe(false);
  });
  it("accepts a reconnect snapshot from a restarted engine", () => {
    const store = new ScreenerStore();
    store.apply({ kind: "delta", topic: "screener.state", payload: screenState({ runId: 99 }) });
    store.apply({ kind: "snapshot", topic: "screener.state", payload: screenState({ runId: 1 }) });
    expect(store.getSnapshot().get("main/screen")?.runId).toBe(1);
  });
});
