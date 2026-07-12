// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { DemoBanner } from "./DemoBanner";
import { SessionStore } from "../data/SessionStore";
import { ThemeProvider } from "./ThemeProvider";
import type { ConnState } from "../wire/WsClient";

function storeWith(mode: "pending" | "live" | "replay" | "demo"): SessionStore {
  const s = new SessionStore();
  if (mode !== "pending") {
    // SessionStore.apply reads m.payload directly (static topic, full replace) —
    // cast bypasses the narrower "live" | "replay" helper type SessionStore.test.ts
    // uses; sys.session snapshots are untyped strings on the wire (see SessionStore.ts).
    s.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } } as never);
  }
  return s;
}

function Wrapped(
  { session, engineState, onGoLive }:
  { session: SessionStore; engineState: ConnState | undefined; onGoLive: () => Promise<void> },
) {
  return (
    <ThemeProvider>
      <DemoBanner session={session} engineState={engineState} onGoLive={onGoLive} />
    </ThemeProvider>
  );
}

describe("DemoBanner", () => {
  it("renders only when session mode is demo", () => {
    render(<Wrapped session={storeWith("demo")} engineState="open" onGoLive={async () => {}} />);
    const banner = screen.getByTestId("demo-banner");
    expect(banner).toBeTruthy();
    expect(banner.textContent).toContain("DEMO");
    expect(banner.textContent).toContain("synthetic market");
    expect(banner.textContent).toContain("practice orders only");
  });

  it("is hidden when session mode is pending (no-flash guard)", () => {
    render(<Wrapped session={storeWith("pending")} engineState="open" onGoLive={async () => {}} />);
    expect(screen.queryByTestId("demo-banner")).toBeNull();
  });

  it("is hidden when session mode is live", () => {
    render(<Wrapped session={storeWith("live")} engineState="open" onGoLive={async () => {}} />);
    expect(screen.queryByTestId("demo-banner")).toBeNull();
  });

  it("is hidden when session mode is replay (mutually exclusive with demo)", () => {
    render(<Wrapped session={storeWith("replay")} engineState="open" onGoLive={async () => {}} />);
    expect(screen.queryByTestId("demo-banner")).toBeNull();
  });

  it("calls onGoLive when Return to live is clicked", () => {
    const onGoLive = vi.fn(async () => {});
    render(<Wrapped session={storeWith("demo")} engineState="open" onGoLive={onGoLive} />);
    fireEvent.click(screen.getByTestId("return-to-live"));
    expect(onGoLive).toHaveBeenCalledTimes(1);
  });

  it("shows the in-flight label until a genuine open->non-open->open cycle completes", async () => {
    const s = storeWith("demo");
    let resolveGoLive: () => void = () => {};
    const onGoLive = vi.fn(() => new Promise<void>((resolve) => { resolveGoLive = resolve; }));

    const { rerender } = render(<Wrapped session={s} engineState="open" onGoLive={onGoLive} />);
    fireEvent.click(screen.getByTestId("return-to-live"));
    expect(screen.getByTestId("return-to-live").textContent).toBe("Returning to live…");

    // Ack resolves ~200ms before the socket actually drops — engineState is
    // still "open" at this point, so the label must NOT clear yet.
    resolveGoLive();
    await Promise.resolve();
    rerender(<Wrapped session={s} engineState="open" onGoLive={onGoLive} />);
    expect(screen.getByTestId("return-to-live").textContent).toBe("Returning to live…");

    // Socket genuinely drops for the restart.
    rerender(<Wrapped session={s} engineState="reconnecting" onGoLive={onGoLive} />);
    expect(screen.getByTestId("return-to-live").textContent).toBe("Returning to live…");

    // Socket reopens post-restart; mode is still "demo" until the new
    // sys.session snapshot lands, so the banner stays but the button resets.
    rerender(<Wrapped session={s} engineState="open" onGoLive={onGoLive} />);
    expect(screen.getByTestId("return-to-live").textContent).toBe("Return to live");
  });
});
