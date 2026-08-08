import { beforeEach, describe, expect, it, vi } from "vitest";
import { initUiLogFromQuery, uiLog } from "./logger";

beforeEach(() => {
  vi.restoreAllMocks();
  initUiLogFromQuery("");
});

describe("uiLog", () => {
  it("uses the selected console method with the prefix and fields", () => {
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    const fields = { error: new Error("boom"), count: 2 };

    uiLog.info("example event", fields);

    expect(info).toHaveBeenCalledWith("[ui]", "example event", fields);
  });

  it("does not emit debug logs by default", () => {
    const debug = vi.spyOn(console, "debug").mockImplementation(() => {});

    uiLog.debug("quiet event");

    expect(debug).not.toHaveBeenCalled();
  });

  it("emits debug logs when ?debug=1 is enabled", () => {
    const debug = vi.spyOn(console, "debug").mockImplementation(() => {});
    initUiLogFromQuery("?debug=1");

    uiLog.debug("verbose event");

    expect(debug).toHaveBeenCalledWith("[ui]", "verbose event");
  });
});
