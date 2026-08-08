import { beforeEach, describe, expect, it, vi } from "vitest";
import { uiLog } from "./logger";

beforeEach(() => vi.restoreAllMocks());

describe("uiLog", () => {
  it("uses the selected console method with the prefix and fields", () => {
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const fields = { error: new Error("boom"), count: 2 };

    uiLog.info("example event", fields);

    expect(info).toHaveBeenCalledWith("[ui]", "example event", fields);
  });
});
