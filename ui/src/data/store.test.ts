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

  it("getRev() supports independent multi-consumer cursors without disturbing the boolean API", () => {
    const p = new P();
    // Two independent consumers, each tracking its own "last seen" cursor.
    let cursorA = p.getRev();
    let cursorB = p.getRev();
    expect(cursorA).toBe(cursorB);

    p.bump();

    // Both consumers observe the single change via getRev(), and neither
    // consuming it resets it for the other (unlike consumeDirty()).
    const changedForA = p.getRev() !== cursorA;
    cursorA = p.getRev();
    const changedForB = p.getRev() !== cursorB;
    cursorB = p.getRev();
    expect(changedForA).toBe(true);
    expect(changedForB).toBe(true);

    // No further change: both cursors now report "no change" independently.
    expect(p.getRev() !== cursorA).toBe(false);
    expect(p.getRev() !== cursorB).toBe(false);

    // The boolean API is untouched by getRev() reads.
    expect(p.isDirty()).toBe(true);
    expect(p.consumeDirty()).toBe(true);
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
