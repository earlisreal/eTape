import { describe, it, expect } from "vitest";
import { reduceTypeToLoad, canStartTypeToLoad, type TypeToLoadState } from "./typeToLoad";

const idle: TypeToLoadState = { editing: false };
const key = (k: string, mod: Partial<{ ctrl: boolean; meta: boolean; alt: boolean }> = {}) =>
  ({ kind: "key" as const, key: k, ctrl: false, meta: false, alt: false, ...mod });

describe("canStartTypeToLoad", () => {
  it("requires active + symbol-bearing, no form field, no modal", () => {
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: false, modalOpen: false })).toBe(true);
    expect(canStartTypeToLoad({ active: false, symbolBearing: true, targetIsFormField: false, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: false, targetIsFormField: false, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: true, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: false, modalOpen: true })).toBe(false);
  });
});

describe("reduceTypeToLoad", () => {
  it("printable char starts editing seeded with the char", () => {
    expect(reduceTypeToLoad(idle, key("n"))).toEqual({ editing: true, draft: "N" });
    expect(reduceTypeToLoad(idle, key("."))).toEqual({ editing: true, draft: "." });
    expect(reduceTypeToLoad(idle, key("5"))).toEqual({ editing: true, draft: "5" });
  });
  it("does not start with a modifier held", () => {
    expect(reduceTypeToLoad(idle, key("n", { ctrl: true }))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("n", { meta: true }))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("n", { alt: true }))).toEqual(idle);
  });
  it("ignores non-printables when idle", () => {
    expect(reduceTypeToLoad(idle, key("Enter"))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("Shift"))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("ArrowUp"))).toEqual(idle);
  });
  it("appends and uppercases while editing", () => {
    const s1 = reduceTypeToLoad({ editing: true, draft: "N" }, key("v"));
    expect(s1).toEqual({ editing: true, draft: "NV" });
  });
  it("Backspace trims; empty draft stays editing", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Backspace"))).toEqual({ editing: true, draft: "N" });
    expect(reduceTypeToLoad({ editing: true, draft: "N" }, key("Backspace"))).toEqual({ editing: true, draft: "" });
  });
  it("Esc/cancel and Enter/commit exit editing", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, { kind: "cancel" })).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Escape"))).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, { kind: "commit" })).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Enter"))).toEqual(idle);
  });
  it("ignores other non-printables while editing (e.g. Tab, arrows) without exiting", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Tab"))).toEqual({ editing: true, draft: "NV" });
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("ArrowLeft"))).toEqual({ editing: true, draft: "NV" });
  });
  it("does not append with a modifier held while editing", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("d", { ctrl: true }))).toEqual({ editing: true, draft: "NV" });
  });
});
