// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { FeedStatusBanner } from "./FeedStatusBanner";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";
import type { ConnState } from "../wire/WsClient";

function storeWith(links: unknown[]): HealthStore {
  const s = new HealthStore();
  // SnapshotMsg/DeltaMsg carry `payload` (NOT `data`); HealthStore reads m.payload.links
  s.apply({ kind: "snapshot", topic: "sys.health", payload: { links } } as never);
  return s;
}

function Wrapped(
  { health, engineState, onOpenConnection }:
  { health: HealthStore; engineState: ConnState; onOpenConnection: () => void },
) {
  return (
    <ThemeProvider>
      <FeedStatusBanner health={health} engineState={engineState} onOpenConnection={onOpenConnection} />
    </ThemeProvider>
  );
}

describe("FeedStatusBanner", () => {
  it("is visible when engineState is open and engine-moomoo is down", () => {
    const s = storeWith([
      { link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" },
    ]);
    render(<Wrapped health={s} engineState="open" onOpenConnection={() => {}} />);
    const banner = screen.getByTestId("feed-status-banner");
    expect(banner).toBeTruthy();
    expect(banner.textContent).toContain("moomoo");
    expect(banner.textContent).toContain("disconnected");
  });

  it("is hidden when engine-moomoo is healthy", () => {
    const s = storeWith([
      { link: "engine-moomoo", ms: 4.2, min: 3, avg: 4, max: 6, status: "ok" },
    ]);
    render(<Wrapped health={s} engineState="open" onOpenConnection={() => {}} />);
    expect(screen.queryByTestId("feed-status-banner")).toBeNull();
  });

  it("is hidden when no sys.health snapshot has arrived yet (no engine-moomoo entry)", () => {
    const s = storeWith([]);
    render(<Wrapped health={s} engineState="open" onOpenConnection={() => {}} />);
    expect(screen.queryByTestId("feed-status-banner")).toBeNull();
  });

  it("is hidden when the WS to the engine is not open, even though moomoo is down (ReconnectOverlay precedence)", () => {
    const s = storeWith([
      { link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" },
    ]);
    render(<Wrapped health={s} engineState="reconnecting" onOpenConnection={() => {}} />);
    expect(screen.queryByTestId("feed-status-banner")).toBeNull();
  });

  it("is hidden when engineState is connecting, even though moomoo is down", () => {
    const s = storeWith([
      { link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" },
    ]);
    render(<Wrapped health={s} engineState="connecting" onOpenConnection={() => {}} />);
    expect(screen.queryByTestId("feed-status-banner")).toBeNull();
  });

  it("calls onOpenConnection when the Connection button is clicked", () => {
    const onOpenConnection = vi.fn();
    const s = storeWith([
      { link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" },
    ]);
    render(<Wrapped health={s} engineState="open" onOpenConnection={onOpenConnection} />);
    fireEvent.click(screen.getByTestId("feed-banner-open-connection"));
    expect(onOpenConnection).toHaveBeenCalled();
  });
});
