// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AlertNavigation } from "./navigation";
import type { LinkGroup } from "../linkGroups";
import type { PanelConfig } from "../workspace";

class Locks implements Pick<LockManager, "request" | "query"> {
  readonly held = new Set<string>();
  private readonly pending = new Map<string, Promise<void>>();
  request<T>(name: string, callback: LockGrantedCallback<T>): Promise<T>;
  request<T>(name: string, options: LockOptions, callback: LockGrantedCallback<T>): Promise<T>;
  async request<T>(name: string, options: LockOptions | LockGrantedCallback<T>, callback?: LockGrantedCallback<T>): Promise<T> {
    const run = typeof options === "function" ? options : callback;
    if (!run) throw new Error("Missing lock callback");
    const previous = this.pending.get(name);
    let release = () => {};
    this.pending.set(name, new Promise<void>((resolve) => { release = resolve; }));
    await previous;
    this.held.add(name);
    try { return await run({ name, mode: "exclusive" }); }
    finally { this.held.delete(name); release(); }
  }
  async query(): Promise<LockManagerSnapshot> {
    return { held: [...this.held].map((name) => ({ name, mode: "exclusive", clientId: name })), pending: [] };
  }
}

let locks: Locks;
const stops: Array<() => void> = [];
const panel = (id: string, group: LinkGroup): PanelConfig => ({ id, panelId: "alerts", group, settings: {} });
function open(owner: string, panels: PanelConfig[], workspace = owner): AlertNavigation {
  const navigation = new AlertNavigation(owner, workspace, { storage: localStorage, locks });
  navigation.sync(panels);
  stops.push(navigation.start());
  return navigation;
}
beforeEach(() => { localStorage.clear(); locks = new Locks(); });
afterEach(async () => { stops.splice(0).forEach((stop) => stop()); await vi.waitFor(() => expect(locks.held.size).toBe(0)); vi.restoreAllMocks(); });

it("prefers the most recently used grouped Alerts Panel across workspaces", async () => {
  const viewer = open("viewer", []);
  const other = open("other", [panel("red", "red"), panel("green", "green"), panel("pinned", null)]);
  other.use("red");
  expect(await viewer.destination("blue")).toEqual({ group: "red", unavailable: false });
  other.use("green");
  other.use("pinned");
  other.use("chart-does-not-change-priority");
  expect(await viewer.destination("blue")).toEqual({ group: "green", unavailable: false });
  other.sync([panel("red", "yellow"), panel("green", null)]);
  expect(await viewer.destination("blue")).toEqual({ group: "yellow", unavailable: false });
  other.sync([]);
  expect(await viewer.destination("blue")).toEqual({ group: "blue", unavailable: false });
});

it("uses current shared snapshots for late joiners and deterministic startup ties", async () => {
  open("z-window", [panel("same", "yellow")], "z-workspace");
  open("a-window", [panel("same", "red")], "a-workspace");
  const late = open("late", []);
  expect(await late.destination("blue")).toEqual({ group: "red", unavailable: false });
});

it("ignores crashed/closed owners without expiring open background windows", async () => {
  const viewer = open("viewer", []);
  const other = open("other", [panel("alerts", "red")]);
  other.use("alerts");
  vi.spyOn(Date, "now").mockReturnValue(Number.MAX_SAFE_INTEGER);
  expect(await viewer.destination("blue")).toEqual({ group: "red", unavailable: false });
  locks.held.delete("etape.alert-panels.other");
  expect(localStorage.getItem("etape.alert-panels.other")).not.toBeNull();
  expect(await viewer.destination("blue")).toEqual({ group: "blue", unavailable: false });
});

it("keeps source-less and ungrouped alerts valid without guessing a color", async () => {
  const viewer = open("viewer", [panel("pinned", null)]);
  expect(await viewer.destination()).toEqual({ group: null, unavailable: false });
  expect(await viewer.destination("purple")).toEqual({ group: null, unavailable: false });
  viewer.sync([panel("pinned", "blue")]);
  expect(await viewer.destination()).toEqual({ group: "blue", unavailable: false });
});

it("does not mistake loading, missing, or invalid owner state for no Alerts Panel", async () => {
  const viewer = open("viewer", []);
  const loading = new AlertNavigation("loading", "other", { storage: localStorage, locks });
  stops.push(loading.start());
  expect(await viewer.destination("blue")).toEqual({ group: null, unavailable: true });
  loading.sync([]);
  expect(await viewer.destination("blue")).toEqual({ group: "blue", unavailable: false });
  localStorage.removeItem("etape.alert-panels.loading");
  expect(await viewer.destination("blue")).toEqual({ group: null, unavailable: true });
  localStorage.setItem("etape.alert-panels.loading", "malformed");
  expect(await viewer.destination("blue")).toEqual({ group: null, unavailable: true });
});

it("fails closed when liveness or shared storage is unavailable", async () => {
  const viewer = open("viewer", []);
  vi.spyOn(locks, "query").mockRejectedValue(new Error("Unavailable"));
  expect(await viewer.destination("blue")).toEqual({ group: null, unavailable: true });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("Quota exceeded"); });
  const blocked = open("blocked", []);
  expect(await blocked.destination("blue")).toEqual({ group: null, unavailable: true });
});

it("releases presence on cleanup and can restart after StrictMode effect replay", async () => {
  const navigation = new AlertNavigation("replay", "main", { storage: localStorage, locks });
  navigation.sync([panel("alerts", "red")]);
  navigation.start()();
  stops.push(navigation.start());
  expect(await navigation.destination("blue")).toEqual({ group: "red", unavailable: false });
  window.dispatchEvent(new Event("pagehide"));
  expect(await navigation.destination("blue")).toEqual({ group: null, unavailable: true });
  await vi.waitFor(() => expect(localStorage.getItem("etape.alert-panels.replay")).toBeNull());
});
