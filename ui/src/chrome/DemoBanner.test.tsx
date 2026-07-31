// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DemoBanner } from "./DemoBanner";
import { SessionStore } from "../data/SessionStore";
import { ThemeProvider } from "./ThemeProvider";

function storeWith(mode: "pending" | "live" | "demo"): SessionStore {
  const s = new SessionStore();
  if (mode !== "pending") s.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } } as never);
  return s;
}

describe("DemoBanner", () => {
  it("renders only when session mode is demo", () => {
    render(<ThemeProvider><DemoBanner session={storeWith("demo")} /></ThemeProvider>);
    const banner = screen.getByTestId("demo-banner");
    expect(banner.textContent).toContain("DEMO");
    expect(banner.textContent).toContain("synthetic market");
  });

  it("is hidden when session mode is pending", () => {
    render(<ThemeProvider><DemoBanner session={storeWith("pending")} /></ThemeProvider>);
    expect(screen.queryByTestId("demo-banner")).toBeNull();
  });

  it("is hidden when session mode is live", () => {
    render(<ThemeProvider><DemoBanner session={storeWith("live")} /></ThemeProvider>);
    expect(screen.queryByTestId("demo-banner")).toBeNull();
  });
});
