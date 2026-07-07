import { describe, it, expect } from "vitest";
import { parseWorkspaceName, nextWindowName } from "./windows";

describe("parseWorkspaceName", () => {
  it("defaults to main when absent", () => expect(parseWorkspaceName("")).toBe("main"));
  it("reads the workspace param", () => expect(parseWorkspaceName("?workspace=window-2")).toBe("window-2"));
  it("sanitizes to [a-z0-9-] and lowercases", () => expect(parseWorkspaceName("?workspace=Win Dow!")).toBe("main"));
});
describe("nextWindowName", () => {
  it("first extra window is window-2", () => expect(nextWindowName(["main"])).toBe("window-2"));
  it("fills the lowest gap", () => expect(nextWindowName(["main", "window-2", "window-4"])).toBe("window-3"));
});
