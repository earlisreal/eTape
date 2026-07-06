// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LadderPanel } from "./LadderPanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf, type Surface } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg } from "../../wire/contract";

beforeEach(() => {
  vi.clearAllMocks();
  cleanup();
});

function renderLadder(settings: Record<string, unknown> = { symbol: "US.AAPL" }) {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  let surface: Surface | undefined;
  const off = vi.fn();
  vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => {
    surface = s;
    return off;
  });
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const config = { id: "t-ladder", panelId: "ladder", group: "green" as const, settings };
  const utils = render(
    <ThemeProvider>
      <LadderPanel config={config} stores={stores} scheduler={scheduler} width={300} height={480}
        linkGroups={linkGroups} commands={{ sendCommand: vi.fn(async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" })), sendQuery: vi.fn(async () => []) }}
        onConfigChange={vi.fn()} />
    </ThemeProvider>,
  );
  return { ...utils, stores, linkGroups, surface: () => surface!, off };
}

describe("LadderPanel", () => {
  it("registers one surface and unregisters it on unmount", () => {
    const { surface, off, unmount } = renderLadder();
    expect(surface().id).toBe("ladder:t-ladder");
    unmount();
    expect(off).toHaveBeenCalledTimes(1);
  });

  it("is dirty after a book update and paints without throwing", () => {
    const { stores, surface } = renderLadder();
    surface().isDirty(); // baseline the rev cursors
    stores.book.apply({
      kind: "snapshot", topic: "md.book",
      payload: { symbol: "US.AAPL", bids: [{ price: 3.49, size: 300 }], asks: [{ price: 3.51, size: 400 }], ts: "t" },
    });
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();
  });

  it("paints the no-entitlement state for non-US symbols without throwing", () => {
    const { surface, linkGroups } = renderLadder();
    linkGroups.focus("green", "HK.00700");
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();
  });

  it("repaints when exec orders change (marks are display-only but live)", () => {
    const { stores, surface } = renderLadder();
    surface().isDirty();
    stores.exec.apply({ kind: "snapshot", topic: "exec.orders",
      payload: [{ symbol: "US.AAPL", price: 3.49, side: "Buy", qty: 100, status: "New" }] });
    expect(surface().isDirty()).toBe(true);
  });
});
