// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { HotkeyDeck, deckToneClass } from "./HotkeyDeck";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { OrderCommands } from "../exec/commands";
import type { ToastApi } from "../Toast";
import type {
  ActionTemplate, OrderConfig, PlaceOrderTemplate, ManagementTemplate, ManagementAction, DeckColor,
} from "../exec/actionTemplate";
import type { AckMsg, Quote, Side } from "../../wire/contract";

const QUOTE: Quote = { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" };

function makeOc(): OrderCommands {
  return {
    submit: vi.fn(async () => {}),
    cancel: vi.fn(async () => {}),
    replace: vi.fn(async () => {}),
    flatten: vi.fn(async () => {}),
    arm: vi.fn(async () => {}),
    disarm: vi.fn(async () => {}),
    kill: vi.fn(async () => {}),
    cancelLast: vi.fn(async () => {}),
    cancelAll: vi.fn(async () => {}),
  } as unknown as OrderCommands;
}
function makeToast(): ToastApi { return { push: vi.fn(), dismiss: vi.fn() }; }

async function setup(templates: ActionTemplate[]) {
  const config: OrderConfig = { activeVenue: "", templates };
  const commands = {
    sendCommand: vi.fn(async (n: string): Promise<AckMsg> => {
      if (n === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: config };
      return { kind: "ack", corrId: "c", status: "accepted", value: undefined };
    }),
  };
  const oc = makeOc();
  const toast = makeToast();
  const onOpenSettings = vi.fn();
  let container!: HTMLElement;
  await act(async () => {
    const r = render(
      <ThemeProvider><OrderConfigProvider commands={commands}>
        <HotkeyDeck venue="alpaca-paper" symbol="US.AAPL" quote={QUOTE} buyingPower={100_000} positionQty={0}
          oc={oc} toast={toast} onOpenSettings={onOpenSettings} />
      </OrderConfigProvider></ThemeProvider>,
    );
    container = r.container;
    await Promise.resolve();
  });
  return { oc, toast, onOpenSettings, container };
}

const place = (over: Partial<PlaceOrderTemplate> = {}): PlaceOrderTemplate => ({
  kind: "place", id: "buy1", label: "Buy 1", side: "BUY", type: "MARKET", tif: "DAY",
  priceSource: "Last", priceOffset: 0, sizing: { mode: "Shares", shares: 1 },
  ...over,
});
const manage = (over: Partial<ManagementTemplate> = {}): ManagementTemplate => ({
  kind: "manage", id: "kill", label: "KILL", action: "KillSwitch",
  ...over,
});

describe("HotkeyDeck — rendering", () => {
  it("renders only deck:true templates, in the config's array order (filtering doesn't reorder)", async () => {
    const t1 = place({ id: "buy1", label: "Buy 1", deck: true });
    const hotkeyOnly = place({ id: "sell-hk", label: "Hotkey Only", side: "SELL", deck: false, hotkey: "Ctrl+9" });
    const t3 = place({ id: "sell1", label: "Sell 1", side: "SELL", deck: true });
    const { container } = await setup([t1, hotkeyOnly, t3]);
    const ids = Array.from(container.querySelectorAll("[data-testid^='deck-']"))
      .map((el) => el.getAttribute("data-testid"));
    expect(ids).toEqual(["deck-buy1", "deck-sell1"]);
    expect(screen.queryByTestId("deck-sell-hk")).toBeNull();
  });

  it("empty state shows the hint and clicking it calls onOpenSettings", async () => {
    const { onOpenSettings } = await setup([]);
    const hint = screen.getByTestId("deck-empty");
    expect(hint.textContent).toContain("Add preset buttons");
    fireEvent.click(hint);
    expect(onOpenSettings).toHaveBeenCalledTimes(1);
  });

  it("renders a Keycap badge only when the template's hotkey is set", async () => {
    const withHotkey = place({ id: "hk", label: "HK", deck: true, hotkey: "Ctrl+1" });
    const noHotkey = place({ id: "nohk", label: "NoHK", deck: true });
    await setup([withHotkey, noHotkey]);
    expect(screen.getByTestId("deck-hk").querySelector("kbd")).not.toBeNull();
    expect(screen.getByTestId("deck-nohk").querySelector("kbd")).toBeNull();
  });
});

describe("HotkeyDeck — click dispatch (real fireTemplate/resolvePlaceTemplate)", () => {
  it("clicking a place-template button calls oc.submit with the args resolvePlaceTemplate produces", async () => {
    const t = place({ id: "buy-ask", label: "Buy Ask", side: "BUY", type: "LIMIT", priceSource: "Ask", sizing: { mode: "Shares", shares: 10 }, deck: true });
    const { oc } = await setup([t]);
    fireEvent.click(screen.getByTestId("deck-buy-ask"));
    const expected = resolvePlaceTemplate(t, {
      venue: "alpaca-paper", symbol: "US.AAPL", quote: QUOTE, buyingPower: 100_000, positionQty: 0, nowMs: 0,
      extHoursMarketBufferPct: 1,
    });
    expect(oc.submit).toHaveBeenCalledTimes(1);
    expect(oc.submit).toHaveBeenCalledWith(expected.args, expected.flash);
  });

  it("deck buttons always submit even when armed would normally gate — no client-side arm gate", async () => {
    // gateArm:false means armed is irrelevant; this just proves the deck path
    // reaches oc.submit unconditionally for a valid place template.
    const t = place({ id: "buy-mkt", label: "Buy Mkt", type: "MARKET", priceSource: "Last", sizing: { mode: "Shares", shares: 1 }, deck: true });
    const { oc } = await setup([t]);
    fireEvent.click(screen.getByTestId("deck-buy-mkt"));
    expect(oc.submit).toHaveBeenCalledTimes(1);
  });

  it("a management template (KillSwitch) with deck:true calls oc.kill() on click", async () => {
    const t = manage({ id: "kill", label: "KILL", action: "KillSwitch", deck: true });
    const { oc, toast } = await setup([t]);
    fireEvent.click(screen.getByTestId("deck-kill"));
    expect(oc.kill).toHaveBeenCalledWith();
    expect(toast.push).toHaveBeenCalledWith({ level: "warn", text: "KILL — cancel-all + disarm" });
  });
});

describe("deckToneClass", () => {
  const sidePlace = (side: Side, deckColor?: DeckColor): PlaceOrderTemplate =>
    place({ side, ...(deckColor !== undefined ? { deckColor } : {}) });
  const sideManage = (action: ManagementAction, deckColor?: DeckColor): ManagementTemplate =>
    manage({ action, ...(deckColor !== undefined ? { deckColor } : {}) });

  it("explicit deckColor overrides win regardless of side/kind", () => {
    expect(deckToneClass(sidePlace("SELL", "green"))).toBe("side side-buy");
    expect(deckToneClass(sidePlace("BUY", "red"))).toBe("side side-sell");
    expect(deckToneClass(sidePlace("BUY", "bronze"))).toBe("side side-bronze");
    expect(deckToneClass(sidePlace("BUY", "neutral"))).toBe("side side-neutral");
    expect(deckToneClass(sideManage("CancelLast", "danger"))).toBe("side side-danger");
  });

  it("auto (or absent) deckColor on a place template follows side", () => {
    expect(deckToneClass(sidePlace("BUY"))).toBe("side side-buy");
    expect(deckToneClass(sidePlace("COVER", "auto"))).toBe("side side-buy");
    expect(deckToneClass(sidePlace("SELL"))).toBe("side side-sell");
    expect(deckToneClass(sidePlace("SHORT", "auto"))).toBe("side side-sell");
  });

  it("auto (or absent) deckColor on a manage template is danger for KillSwitch, neutral otherwise", () => {
    expect(deckToneClass(sideManage("KillSwitch"))).toBe("side side-danger");
    expect(deckToneClass(sideManage("CancelLast", "auto"))).toBe("side side-neutral");
    expect(deckToneClass(sideManage("CancelAllFocused"))).toBe("side side-neutral");
    expect(deckToneClass(sideManage("CancelAllEverything"))).toBe("side side-neutral");
  });
});
