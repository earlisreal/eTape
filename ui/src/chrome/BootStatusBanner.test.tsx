// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { BootStatusBanner } from "./BootStatusBanner";
import { BootStore } from "../data/BootStore";
import { ThemeProvider } from "./ThemeProvider";

function withStore(phase: "connecting" | "sealing" | "ready", daysTotal = 0) {
  const boot = new BootStore();
  boot.apply({ kind: "snapshot", topic: "sys.boot", payload: { phase, daysTotal } });
  return boot;
}

describe("BootStatusBanner", () => {
  it("shows a sealing message", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("sealing", 2)} /></ThemeProvider>);
    expect(screen.getByTestId("boot-status-banner").textContent).toMatch(/compressing 2 days/i);
  });
  it("shows a connecting message", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("connecting")} /></ThemeProvider>);
    expect(screen.getByTestId("boot-status-banner").textContent).toMatch(/connecting to market data/i);
  });
  it("hides when ready", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("ready")} /></ThemeProvider>);
    expect(screen.queryByTestId("boot-status-banner")).toBeNull();
  });
});
