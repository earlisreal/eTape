import { describe, it, expect, vi } from "vitest";
import { PaintStore, ReactStore } from "./store";

class P extends PaintStore { bump() { this.markDirty(); } }
class R extends ReactStore<number> {
  constructor() { super(0); }
  inc() { this.set(this.getSnapshot() + 1); }
}

describe("PaintStore", () => {
  it("tracks and consumes the dirty flag", () => {
    const p = new P();
    expect(p.isDirty()).toBe(false);
    p.bump();
    expect(p.isDirty()).toBe(true);
    expect(p.consumeDirty()).toBe(true);
    expect(p.isDirty()).toBe(false);
    expect(p.consumeDirty()).toBe(false);
  });
});

describe("ReactStore", () => {
  it("notifies subscribers and returns a stable snapshot", () => {
    const r = new R();
    const cb = vi.fn();
    const off = r.subscribe(cb);
    const before = r.getSnapshot();
    r.inc();
    expect(cb).toHaveBeenCalledTimes(1);
    expect(r.getSnapshot()).toBe(before + 1);
    off();
    r.inc();
    expect(cb).toHaveBeenCalledTimes(1); // no longer subscribed
  });
});
