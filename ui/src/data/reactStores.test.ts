import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";

describe("ExecStore", () => {
  it("replaces account on snapshot", () => {
    const s = new ExecStore();
    s.apply({ kind: "snapshot", topic: "exec.account", payload: { equity: 1000, armed: false } });
    expect(s.getSnapshot().account).toMatchObject({ equity: 1000, armed: false });
  });
});
