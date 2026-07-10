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

  it("isDirty reacts only to its own pinned symbol's book/tape revisions, not another symbol's (the per-symbol scoping this migration exists to add)", () => {
    const { stores, surface } = renderLadder(); // pinned to US.AAPL via settings.symbol
    surface().isDirty(); // baseline the rev cursors

    // A different symbol's book delta must NOT dirty a panel pinned to US.AAPL —
    // this is the actual bug this task fixes (isDirty used to read a global rev).
    stores.book.apply({
      kind: "snapshot", topic: "md.book",
      payload: { symbol: "US.NVDA", bids: [{ price: 400, size: 10 }], asks: [{ price: 401, size: 10 }], ts: "t" },
    });
    expect(surface().isDirty()).toBe(false);

    // Nor must a different symbol's tape delta.
    stores.tape.apply({ kind: "delta", topic: "md.tape",
      payload: [{ symbol: "US.NVDA", price: 400.5, size: 50, direction: "BUY", ts: "t" }] });
    expect(surface().isDirty()).toBe(false);

    // The pinned symbol's own book delta must dirty it.
    stores.book.apply({
      kind: "snapshot", topic: "md.book",
      payload: { symbol: "US.AAPL", bids: [{ price: 3.49, size: 300 }], asks: [{ price: 3.51, size: 400 }], ts: "t" },
    });
    expect(surface().isDirty()).toBe(true);
    surface().isDirty(); // consume, re-baseline

    // The pinned symbol's own tape delta must also dirty it.
    stores.tape.apply({ kind: "delta", topic: "md.tape",
      payload: [{ symbol: "US.AAPL", price: 3.5, size: 100, direction: "SELL", ts: "t" }] });
    expect(surface().isDirty()).toBe(true);
  });

  // Regression guard for reseedForGroup's `tapeGen = stores.tape.generation(symbol)`
  // line (LadderPanel.tsx). Without it, tapeGen stays pinned to the OLD symbol's
  // generation after a group re-pick, so paint()'s reconnect-detection branch
  // (`if (tapeGen !== stores.tape.generation(symbol))`) misfires for the new
  // symbol on its very first live tick — re-seeding as if THAT tick were history
  // instead of flashing it as a live print. Distinguishing observable: the
  // reconnect branch's own `seedLast()` call consumes the just-applied tick's seq
  // as the new baseline, so the normal tick-walk loop below it sees no new ticks
  // to flash — `flash` stays null and the flash-driven isDirty() persistence
  // (`flashAlpha(flash, now) > 0`) never kicks in, even though `last` still looks
  // correct. `last` alone can't tell correct from buggy here; only the flash can.
  it("refreshes tapeGen to the new symbol on a group switch, so the new symbol's first live tick flashes normally instead of misfiring the reconnect re-seed", () => {
    const { stores, surface, linkGroups } = renderLadder(); // pinned to US.AAPL via settings.symbol
    surface().isDirty(); // baseline the rev cursors

    // Give the OLD symbol (US.AAPL) a non-zero generation via a genuine reconnect
    // (a snapshot frame), and let the panel catch up to it via a real paint() —
    // this is legitimate, correct behavior, and it's what makes the OLD symbol's
    // tapeGen non-zero so a later "stuck at the old value" bug is observable
    // (both symbols defaulting to generation 0 would hide the bug).
    stores.tape.apply({
      kind: "snapshot", topic: "md.tape",
      payload: [{ symbol: "US.AAPL", price: 190, size: 10, direction: "BUY", ts: "t1" }],
    });
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();
    expect(surface().isDirty()).toBe(false); // quiescent again: no lingering flash/dirty state

    // Group re-pick: switch the panel from US.AAPL to US.NVDA, a symbol that has
    // never been snapshotted (generation 0) — the scenario reseedForGroup must
    // handle by refreshing tapeGen to the new symbol's own generation.
    linkGroups.focus("green", "US.NVDA");
    expect(surface().isDirty()).toBe(true); // reseed force-bumped the surface

    // A genuine live tick for the NEW symbol, exactly like a real print arriving
    // right after the switch — this is what a correctly-reseeded tapeGen must
    // treat as a normal update, not a stale-reconnect misfire.
    stores.tape.apply({
      kind: "delta", topic: "md.tape",
      payload: [{ symbol: "US.NVDA", price: 401.25, size: 50, direction: "SELL", ts: "t2" }],
    });
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();

    // The critical assertion: a correctly-reseeded tapeGen matches US.NVDA's
    // (unchanged) generation, so paint() takes the normal tick-walk path and sets
    // a fresh flash for the new tick — isDirty() reports true purely from that
    // flash's decay window, with no other store revision having changed since
    // the previous isDirty() call. A stale tapeGen (bug reintroduced) instead
    // trips the reconnect branch, whose own seedLast() call swallows the tick's
    // seq before the tick-walk loop can flash it, leaving isDirty() false here.
    expect(surface().isDirty()).toBe(true);
  });
});
