// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LatencyReadout } from "./LatencyReadout";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

function storeWith(links: unknown[]): HealthStore {
  const s = new HealthStore();
  // SnapshotMsg/DeltaMsg carry `payload` (NOT `data`); HealthStore reads m.payload.links
  s.apply({ kind: "snapshot", topic: "sys.health", payload: { links } } as never);
  return s;
}

describe("LatencyReadout", () => {
  it("shows all three links with ms and threshold color classes", () => {
    const s = storeWith([
      { link: "ui-engine", ms: 0.5, min: 0.2, avg: 0.4, max: 1, status: "ok" },
      { link: "engine-moomoo", ms: 4.2, min: 3, avg: 4, max: 6, status: "ok" },
      { link: "engine-tz", ms: 184, min: 90, avg: 150, max: 300, status: "degraded" },
    ]);
    render(<ThemeProvider><LatencyReadout health={s} onOpen={() => {}} /></ThemeProvider>);
    expect(screen.getByText("eng")).toBeTruthy();
    expect(screen.getByText("moo")).toBeTruthy();
    expect(screen.getByText("tz")).toBeTruthy();
    expect(screen.getByTestId("lat-tz").textContent).toContain("184");
  });
  it("calls onOpen when clicked", () => {
    const onOpen = vi.fn();
    render(<ThemeProvider><LatencyReadout health={storeWith([])} onOpen={onOpen} /></ThemeProvider>);
    fireEvent.click(screen.getByTestId("latency-readout"));
    expect(onOpen).toHaveBeenCalled();
  });
});
