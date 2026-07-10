import { describe, it, expect, vi } from "vitest";
import { connectEventToasts } from "./quotaToasts";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

function makeClient() {
  let handler: (m: SnapshotMsg | DeltaMsg) => void = () => {};
  return {
    subscribe: (_t: "sys.events", cb: (m: SnapshotMsg | DeltaMsg) => void) => {
      handler = cb;
      return () => {};
    },
    emit: (m: SnapshotMsg | DeltaMsg) => handler(m),
  };
}
const delta = (seq: number, level: string, detail: string): DeltaMsg => ({
  kind: "delta", topic: "sys.events",
  payload: { seq, ts: `t${seq}`, kind: "quota", detail, level },
});

describe("connectEventToasts", () => {
  it("toasts warn/danger deltas once, skips snapshots and info", () => {
    const c = makeClient();
    const push = vi.fn();
    connectEventToasts(c as never, { push, dismiss: vi.fn() });

    // snapshot (history replay) must not toast
    c.emit({ kind: "snapshot", topic: "sys.events",
      payload: [{ seq: 1, ts: "t1", kind: "quota", detail: "old", level: "warn" }] });
    expect(push).not.toHaveBeenCalled();

    c.emit(delta(2, "warn", "8 subscription slots remaining account-wide"));
    c.emit(delta(2, "warn", "8 subscription slots remaining account-wide")); // redelivered => deduped
    c.emit(delta(3, "danger", "subscription quota exhausted account-wide"));
    c.emit(delta(4, "info", "another OpenD client is using 15 subscription slots")); // info => no toast

    expect(push).toHaveBeenCalledTimes(2);
    expect(push).toHaveBeenNthCalledWith(1, { level: "warn", text: "8 subscription slots remaining account-wide" });
    expect(push).toHaveBeenNthCalledWith(2, { level: "danger", text: "subscription quota exhausted account-wide" });
  });

  it("does not double-toast an event delivered via snapshot then redelivered via delta", () => {
    const c = makeClient();
    const push = vi.fn();
    connectEventToasts(c as never, { push, dismiss: vi.fn() });

    // snapshot replay (e.g. on reconnect) includes a warn event already seen live
    c.emit({ kind: "snapshot", topic: "sys.events",
      payload: [{ seq: 1, ts: "t1", kind: "quota", detail: "old", level: "warn" }] });
    expect(push).not.toHaveBeenCalled();

    // same event redelivered via delta (identical kind/seq/ts) must be deduped
    // against the snapshot-seen key, not just against prior deltas
    c.emit({ kind: "delta", topic: "sys.events",
      payload: { seq: 1, ts: "t1", kind: "quota", detail: "old", level: "warn" } });

    expect(push).not.toHaveBeenCalled();
  });
});
