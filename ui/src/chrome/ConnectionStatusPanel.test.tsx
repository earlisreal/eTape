// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { HealthStore } from "../data/HealthStore";
import { ConnectionStatusPanel } from "./panels/ConnectionStatusPanel";

describe("ConnectionStatusPanel", () => {
  it("renders latency rows and appends events from the store", () => {
    const health = new HealthStore();
    render(<ConnectionStatusPanel health={health} />);
    act(() => {
      health.apply({ kind: "snapshot", topic: "sys.health",
        payload: { links: [{ link: "engine-moomoo", ms: 12, min: 8, avg: 12, max: 20, status: "ok" }] } });
      health.apply({ kind: "delta", topic: "sys.events",
        payload: { seq: 1, ts: "t1", kind: "boot", detail: "engine started" } });
    });
    expect(screen.getByText(/engine-moomoo/)).toBeTruthy();
    expect(screen.getAllByText(/12/).length).toBeGreaterThan(0);
    expect(screen.getByText(/engine started/)).toBeTruthy();
  });

  it("does not crash on a pre-first-poll null links payload (zero-value engine snapshot)", () => {
    const health = new HealthStore();
    render(<ConnectionStatusPanel health={health} />);
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
    render(<ConnectionStatusPanel health={health} />);
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
});
