// ui/src/chrome/panels/TapePanel.test.tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, screen, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { TapePanel } from "./TapePanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf, type Surface } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { Tick, AckMsg } from "../../wire/contract";

beforeEach(() => {
  vi.clearAllMocks();
  cleanup();
});

function mkTick(i: number): Tick {
  return { symbol: "US.AAPL", price: 3.5, size: 100 + i, direction: "BUY", ts: "2026-07-06T13:30:00Z" };
}

function renderTape() {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  let surface: Surface | undefined;
  const off = vi.fn();
  vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => {
    surface = s;
    return off;
  });
  const onConfigChange = vi.fn();
  const config = { id: "t-tape", panelId: "tape", group: "green" as const, settings: { symbol: "US.AAPL", minSize: 0 } };
  const utils = render(
    <ThemeProvider>
      <TapePanel config={config} stores={stores} scheduler={scheduler} width={260} height={400}
        linkGroups={new LinkGroups(new BroadcastChannelBus(), () => {})}
        commands={{ sendCommand: vi.fn(async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" })), sendQuery: vi.fn(async () => []) }}
        onConfigChange={onConfigChange} />
    </ThemeProvider>,
  );
  const canvas = utils.container.querySelector("canvas")!;
  return { ...utils, stores, canvas, surface: () => surface!, off, onConfigChange };
}

describe("TapePanel", () => {
  it("registers one surface and unregisters on unmount", () => {
    const { surface, off, unmount } = renderTape();
    expect(surface().id).toBe("tape:t-tape");
    unmount();
    expect(off).toHaveBeenCalledTimes(1);
  });

  it("persists the min-size filter through onConfigChange", () => {
    const { onConfigChange } = renderTape();
    fireEvent.change(screen.getByLabelText(/min size/i), { target: { value: "250" } });
    // Patch-only: AppShell merges patches, so the panel sends just the key it
    // changed (a full-settings rewrite from frozen config clobbered siblings).
    expect(onConfigChange).toHaveBeenCalledWith({ minSize: 250 });
  });

  it("wheel-up pauses (pill appears); jump to live resumes", () => {
    const { stores, canvas } = renderTape();
    stores.tape.apply({ kind: "snapshot", topic: "md.tape",
      payload: Array.from({ length: 30 }, (_, i) => mkTick(i)) });
    expect(screen.queryByText(/jump to live/i)).toBeNull();
    fireEvent.wheel(canvas, { deltaY: -54 }); // 3 rows up at TAPE_ROW_H = 18
    expect(screen.getByText(/jump to live/i)).toBeTruthy();
    fireEvent.click(screen.getByText(/jump to live/i));
    expect(screen.queryByText(/jump to live/i)).toBeNull();
  });

  it("paints without throwing on an empty ring", () => {
    const { surface } = renderTape();
    expect(() => surface().paint()).not.toThrow();
  });

  it("drops the paused pill once the anchor ages out of the retained ring (Task 7 eviction fix, mirrored at the panel level)", () => {
    // Same regression as tapeState.test.ts's "an anchor that aged out of the retained
    // ring renders live (eviction honesty)" — but here we drive it through the real
    // TapeRing (capacity 65536) and assert the panel's own paused-pill state, not just
    // buildTapeRows's return value. A long pause + a burst large enough to overrun the
    // ring's capacity evicts the anchored row without touching generation (no reconnect).
    const { stores, canvas, surface } = renderTape();
    stores.tape.apply({ kind: "snapshot", topic: "md.tape",
      payload: Array.from({ length: 30 }, (_, i) => mkTick(i)) });
    fireEvent.wheel(canvas, { deltaY: -54 }); // pauses 3 rows back (anchorSeq 27)
    expect(screen.getByText(/jump to live/i)).toBeTruthy();

    // Burst past the ring's capacity so the anchored seq (27) is overwritten — same
    // generation throughout (delta, not snapshot), so this exercises the eviction path
    // distinct from the already-covered reconnect/generation-mismatch branch.
    stores.tape.apply({ kind: "delta", topic: "md.tape",
      payload: Array.from({ length: 65600 }, (_, i) => mkTick(1000 + i)) });

    act(() => {
      surface().paint();
    });
    expect(screen.queryByText(/jump to live/i)).toBeNull();
  });
});
