import { describe, it, expect, afterEach } from "vitest";
import { modalTracker } from "./modalTracker";

describe("modalTracker", () => {
  afterEach(() => modalTracker.setOpen(false)); // module-level singleton — don't leak into other test files

  it("starts closed and reflects setOpen", () => {
    expect(modalTracker.isOpen()).toBe(false);
    modalTracker.setOpen(true);
    expect(modalTracker.isOpen()).toBe(true);
    modalTracker.setOpen(false);
    expect(modalTracker.isOpen()).toBe(false);
  });

  it("notifies subscribers only on an actual change", () => {
    let calls = 0;
    const unsub = modalTracker.subscribe(() => calls++);
    modalTracker.setOpen(true);
    modalTracker.setOpen(true); // no-op, already open
    expect(calls).toBe(1);
    modalTracker.setOpen(false);
    expect(calls).toBe(2);
    unsub();
    modalTracker.setOpen(true);
    expect(calls).toBe(2); // unsubscribed — no further notifications
  });
});
