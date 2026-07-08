// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { HealthStore } from "../data/HealthStore";
import { ConnectionStatusPanel } from "./panels/ConnectionStatusPanel";
import { ThemeProvider } from "./ThemeProvider";
import { getPalette } from "../render/palette";

// jsdom normalizes inline hex colors to rgb(); compare against that form.
function toRgb(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
}

function wrap(health: HealthStore) {
  return render(
    <ThemeProvider>
      <ConnectionStatusPanel health={health} />
    </ThemeProvider>,
  );
}

describe("ConnectionStatusPanel", () => {
  it("renders latency rows and appends events from the store", () => {
    const health = new HealthStore();
    wrap(health);
    act(() => {
      health.apply({ kind: "snapshot", topic: "sys.health",
        payload: { links: [{ link: "engine-moomoo", ms: 12, min: 8, avg: 12, max: 20, status: "ok" }] } });
      health.apply({ kind: "delta", topic: "sys.events",
        payload: { seq: 1, ts: "t1", kind: "boot", detail: "engine started" } });
    });
    expect(screen.getByText(/engine-moomoo/)).toBeTruthy();
    expect(screen.getAllByText(/12/).length).toBeGreaterThan(0);
    expect(screen.getByText(/engine started/)).toBeTruthy();
    const dot = screen.getByText("●");
    expect(dot.style.color).toBe(toRgb(getPalette("light").ok));
  });

  it("colors a degraded link with the warn palette color and a down link with danger", () => {
    const health = new HealthStore();
    wrap(health);
    act(() => {
      health.apply({ kind: "snapshot", topic: "sys.health",
        payload: { links: [
          { link: "engine-moomoo", ms: 40, min: 8, avg: 12, max: 40, status: "degraded" },
          { link: "engine-tz", ms: null, min: null, avg: null, max: null, status: "down" },
        ] } });
    });
    const dots = screen.getAllByText("●");
    expect(dots[0].style.color).toBe(toRgb(getPalette("light").warn));
    expect(dots[1].style.color).toBe(toRgb(getPalette("light").danger));
  });

  it("does not crash on a pre-first-poll null links payload (zero-value engine snapshot)", () => {
    const health = new HealthStore();
    wrap(health);
    expect(() => {
      act(() => {
        health.apply({
          kind: "snapshot",
          topic: "sys.health",
          payload: { links: null },
        } as unknown as Parameters<typeof health.apply>[0]);
      });
    }).not.toThrow();
    const table = screen.getByRole("table");
    expect(table.querySelectorAll("tbody tr")).toHaveLength(0);
  });

  it("does not crash on a pre-first-poll null sys.events payload (nil Go slice, zero-value engine snapshot)", () => {
    const health = new HealthStore();
    wrap(health);
    expect(() => {
      act(() => {
        health.apply({
          kind: "delta",
          topic: "sys.events",
          payload: null,
        } as unknown as Parameters<typeof health.apply>[0]);
      });
    }).not.toThrow();
  });

  it("renders both events when the Hub's sysEventSeq and health.Poller's seq collide (same seq, different kind)", () => {
    // The two counters are independent (Hub-owned ui-drop events vs.
    // health.Poller's own events), so seq alone can't disambiguate them.
    // Regression coverage for the React duplicate-key risk this produces.
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const health = new HealthStore();
    wrap(health);
    act(() => {
      health.apply({ kind: "delta", topic: "sys.events",
        payload: { seq: 1, ts: "t1", kind: "boot", detail: "engine started" } });
      health.apply({ kind: "delta", topic: "sys.events",
        payload: { seq: 1, ts: "t2", kind: "ui-drop", detail: "dropped UI client 3: write timeout" } });
    });
    expect(screen.getByText(/engine started/)).toBeTruthy();
    expect(screen.getByText(/dropped UI client 3: write timeout/)).toBeTruthy();
    const keyWarning = consoleError.mock.calls.some((args) =>
      args.some((a) => typeof a === "string" && a.includes("two children with the same key")),
    );
    expect(keyWarning).toBe(false);
    consoleError.mockRestore();
  });
});
