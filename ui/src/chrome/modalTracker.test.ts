import { describe, it, expect, afterEach } from "vitest";
import { modalTracker } from "./modalTracker";

describe("modalTracker", () => {
  // module-level singleton — don't leak state into other tests/files. Two
  // `setOpen(false)` calls fully drain the counter regardless of how many
  // "owners" a given test opened (floored at 0, so extra closes are safe).
  afterEach(() => {
    modalTracker.setOpen(false);
    modalTracker.setOpen(false);
  });

  it("starts closed and reflects setOpen", () => {
    expect(modalTracker.isOpen()).toBe(false);
    modalTracker.setOpen(true);
    expect(modalTracker.isOpen()).toBe(true);
    modalTracker.setOpen(false);
    expect(modalTracker.isOpen()).toBe(false);
  });

  it("setOpen(false) on an already-closed tracker is a no-op (floored at 0, no negative count)", () => {
    let calls = 0;
    const unsub = modalTracker.subscribe(() => calls++);
    expect(modalTracker.isOpen()).toBe(false);
    modalTracker.setOpen(false); // stray/unbalanced extra close
    expect(modalTracker.isOpen()).toBe(false);
    expect(calls).toBe(0); // no transition, so no notification
    // A single subsequent open still works — the stray close above didn't
    // require a compensating extra `true` to look "open" again.
    modalTracker.setOpen(true);
    expect(modalTracker.isOpen()).toBe(true);
    expect(calls).toBe(1);
    unsub();
  });

  it("is reference-counted: two independent owners both holding it open means only the LAST close actually closes it, and subscribers only see the real transition", () => {
    let calls = 0;
    const unsub = modalTracker.subscribe(() => calls++);

    // Owner 1: e.g. AppShell's Settings modal opens.
    modalTracker.setOpen(true);
    expect(modalTracker.isOpen()).toBe(true);
    expect(calls).toBe(1); // closed -> open transition

    // Owner 2: e.g. a TVDialog-based dialog opens on top of Settings.
    modalTracker.setOpen(true);
    expect(modalTracker.isOpen()).toBe(true);
    expect(calls).toBe(1); // already open — no additional notification

    // Owner 2 closes (e.g. the dialog unmounts) while owner 1 is still open.
    // This must NOT close the tracker or notify subscribers — Settings still
    // has the user's attention and type-to-load must stay suppressed.
    modalTracker.setOpen(false);
    expect(modalTracker.isOpen()).toBe(true);
    expect(calls).toBe(1);

    // Owner 1 (Settings) finally closes too — only now does it actually close,
    // and only now do subscribers see the open -> closed transition.
    modalTracker.setOpen(false);
    expect(modalTracker.isOpen()).toBe(false);
    expect(calls).toBe(2);

    unsub();
    modalTracker.setOpen(true);
    expect(calls).toBe(2); // unsubscribed — no further notifications
  });
});
