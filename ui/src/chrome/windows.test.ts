import { afterEach, describe, it, expect, vi } from "vitest";
import { focusMainWorkspace, parseWorkspaceName, nextWindowName } from "./windows";

afterEach(() => vi.unstubAllGlobals());

function stubBrowser(search = "?workspace=window-2") {
  const open = vi.fn();
  vi.stubGlobal("window", {
    location: { search, href: "http://localhost:8686?debug=1" },
    screen: { availWidth: 1920, availHeight: 1080 },
    innerWidth: 1280,
    innerHeight: 720,
    open,
  });
  return { open };
}
describe("parseWorkspaceName", () => {
  it("defaults to main when absent", () => expect(parseWorkspaceName("")).toBe("main"));
  it("reads the workspace param", () => expect(parseWorkspaceName("?workspace=window-2")).toBe("window-2"));
  it("accepts stable UUID workspace IDs", () => expect(parseWorkspaceName("?workspace=123e4567-e89b-12d3-a456-426614174000")).toBe("123e4567-e89b-12d3-a456-426614174000"));
  it("sanitizes to [a-z0-9-] and lowercases", () => expect(parseWorkspaceName("?workspace=Win Dow!")).toBe("main"));
});
describe("nextWindowName", () => {
  it("first extra window is window-2", () => expect(nextWindowName(["main"])).toBe("window-2"));
  it("fills the lowest gap", () => expect(nextWindowName(["main", "window-2", "window-4"])).toBe("window-3"));
});

describe("focusMainWorkspace", () => {
  it("does nothing when the Scanner already lives in main", () => {
    const { open } = stubBrowser("?workspace=main");
    focusMainWorkspace();
    expect(open).not.toHaveBeenCalled();
  });

  it("reuses and focuses main without navigating an existing window", () => {
    const { open } = stubBrowser();
    const main = { location: { href: "http://localhost:8686?debug=1" }, focus: vi.fn() };
    open.mockReturnValue(main);

    focusMainWorkspace();

    expect(open).toHaveBeenCalledWith("", "etape-workspace-main", expect.any(String));
    expect(main.location.href).toBe("http://localhost:8686?debug=1");
    expect(main.focus).toHaveBeenCalledOnce();
  });

  it("opens and focuses main when the named window is closed", () => {
    const { open } = stubBrowser();
    const main = { location: { href: "about:blank" }, focus: vi.fn() };
    open.mockReturnValue(main);

    focusMainWorkspace();

    expect(main.location.href).toBe("http://localhost:8686/?workspace=main");
    expect(main.focus).toHaveBeenCalledOnce();
  });

  it("leaves symbol activation safe when the browser blocks the popup", () => {
    const { open } = stubBrowser();
    open.mockReturnValue(null);

    expect(() => focusMainWorkspace()).not.toThrow();
  });
});
