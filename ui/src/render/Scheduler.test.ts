import { describe, it, expect, vi } from "vitest";
import { Scheduler } from "./Scheduler";
import { FakeRaf } from "../../test/fakes";
import type { Surface } from "./surface";

function surf(id: string, dirty: () => boolean, paint: () => void): Surface {
  return { id, isDirty: dirty, paint };
}

describe("Scheduler", () => {
  it("paints only dirty surfaces, once per frame", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    let dirtyA = true;
    const paintA = vi.fn(() => { dirtyA = false; });
    const paintB = vi.fn();
    sched.register(surf("a", () => dirtyA, paintA));
    sched.register(surf("b", () => false, paintB));
    sched.start();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1);
    expect(paintB).not.toHaveBeenCalled();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1); // no longer dirty
  });

  it("unregisters a painter that throws and reports it, others survive", () => {
    const raf = new FakeRaf();
    const onErr = vi.fn();
    const sched = new Scheduler(raf, onErr);
    const good = vi.fn();
    sched.register(surf("bad", () => true, () => { throw new Error("boom"); }));
    sched.register(surf("good", () => true, good));
    sched.start();
    raf.tick();
    expect(onErr).toHaveBeenCalledWith("bad", expect.any(Error));
    expect(good).toHaveBeenCalledTimes(1);
    raf.tick();
    expect(good).toHaveBeenCalledTimes(2); // bad no longer scheduled; good keeps painting
  });

  it("stops requesting frames after stop()", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    const paint = vi.fn();
    sched.register(surf("a", () => true, paint));
    sched.start();
    raf.tick();
    sched.stop();
    raf.tick();
    expect(paint).toHaveBeenCalledTimes(1);
  });
});
