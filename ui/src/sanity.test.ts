import { describe, it, expect } from "vitest";

describe("toolchain sanity", () => {
  it("runs typed TypeScript under vitest", () => {
    const n: number = [1, 2, 3].reduce((a, b) => a + b, 0);
    expect(n).toBe(6);
  });
});
