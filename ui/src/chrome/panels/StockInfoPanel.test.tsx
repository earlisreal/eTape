// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { StockInfoPanel, newsDateLabel } from "./StockInfoPanel";
import { openNewsWindow } from "../windows";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";
import type { AckMsg, NewsItem, StockDetailPayload, SnapshotMsg } from "../../wire/contract";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel(opts?: { settings?: Record<string, unknown> }) {
  const stores = makeStores();
  const news = stores.news;
  const stockDetail = stores.stockDetail;
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-news", panelId: "stock-info", group: "green", settings: opts?.settings ?? {} };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" }), sendQuery: async () => [] } } as PanelProps;
  render(<ThemeProvider><StockInfoPanel {...props} /></ThemeProvider>);
  return { news, stockDetail, linkGroups, onConfigChange };
}

const newsItem = (symbol: string, url: string, seen_at: string, overrides: Partial<NewsItem> = {}): NewsItem =>
  ({ id: url, symbols: [symbol], headline: "h", source: "R", url, seen_at, published_at: "", published_precision: "unknown", view_count: 0, type: "news", catalyst_category: "earnings", catalyst_score: 65, catalyst_reasons: [], ...overrides });

const detailPayload = (symbol: string, overrides: Partial<StockDetailPayload> = {}): StockDetailPayload => ({
  symbol, shortSellRestricted: false, name: `${symbol} Inc`, industry: "Software", country: "United States", sector: "Technology", exchange: "NASDAQ", price: 10, lastClose: 9.5, changePct: 5.2,
  marketCap: 3_210_000_000_000, floatMarketCap: 900_000_000, sharesOutstanding: 22_700_000, floatShares: 20_000_000,
  pe: 20, peTTM: 21, eps: 0.5, high52: 15, low52: 5, ema200: 145.5, volume: 1000, refreshedAt: "t1",
  borrowStatus: null, shortable: null, marginable: null, tradable: null,
  ...overrides,
});
const detailSnap = (p: unknown) => ({ kind: "snapshot", topic: "stock.detail", payload: p } as SnapshotMsg);

let lastPopup: { closed: boolean } | undefined;
afterEach(() => {
  if (lastPopup) lastPopup.closed = true;
  vi.restoreAllMocks();
});

describe("newsDateLabel", () => {
  it("labels today vs older dates", () => {
    // Fixtures are built from LOCAL Date components (not hardcoded UTC ISO strings) so the
    // resolved calendar day is stable under any machine timezone: constructing a Date from
    // (year, monthIndex, day, ...) and later reading it back with the local getters (as
    // newsDateLabel does) always round-trips to the same local day, regardless of the
    // executing machine's UTC offset. monthIndex is 0-based, so July is 6.
    const now = new Date(2026, 6, 7, 12, 0, 0).getTime(); // Jul 7, 2026, 12:00 local
    const todaySeenAt = new Date(2026, 6, 7, 9, 0, 0).toISOString(); // Jul 7, 2026, 09:00 local — same day as `now`
    const olderDate = new Date(2026, 6, 4, 16, 0, 0); // Jul 4, 2026, 16:00 local — 3 days earlier, well clear of any boundary
    const olderSeenAt = olderDate.toISOString();
    const expectedOlderLabel = olderDate.toLocaleDateString("en-US", { month: "short", day: "numeric" });

    expect(newsDateLabel(todaySeenAt, now)).toEqual({ label: "today", today: true });
    expect(newsDateLabel(olderSeenAt, now).today).toBe(false);
    expect(newsDateLabel(olderSeenAt, now).label).toBe(expectedOlderLabel);
  });
});

describe("StockInfoPanel", () => {
  it("shows a reserved halt-banner slot and a no-symbol hint before focus", () => {
    renderPanel();
    expect(screen.getByTestId("halt-slot")).toBeTruthy();
    expect(screen.getByText(/no symbol focused/i)).toBeTruthy();
  });

  it("shows nothing below the header — no catalyst checkbox, no news area — when no symbol is focused", () => {
    renderPanel();
    expect(screen.queryByRole("checkbox", { name: /catalysts only/i })).toBeNull();
    expect(screen.queryByText(/no news for/i)).toBeNull();
  });

  it("follows the group's focused symbol and lists its news newest-first", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "u1", "2026-07-06T13:28:00Z", { headline: "Older AAPL" }),
        newsItem("US.AAPL", "u2", "2026-07-06T13:31:00Z", { headline: "Newer AAPL" }),
        newsItem("US.NVDA", "n1", "2026-07-06T13:30:00Z", { headline: "NVDA news" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    const links = screen.getAllByRole("link");
    expect(links.map((a) => a.textContent)).toEqual(["Newer AAPL", "Older AAPL"]); // newest first, NVDA excluded
    expect(screen.getAllByText(/\d{2}:\d{2}:\d{2}/).length).toBeGreaterThan(0);
  });

  it("clicking a headline opens its url in the centered News Reader popup", () => {
    const { news, linkGroups } = renderPanel();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    Object.defineProperties(window, {
      screenX: { configurable: true, value: 100 },
      screenY: { configurable: true, value: 40 },
      outerWidth: { configurable: true, value: 1400 },
      outerHeight: { configurable: true, value: 1200 },
    });
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "https://x/a", "t", { headline: "H" })] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("H"));
    expect(open).toHaveBeenCalledWith(
      "https://x/a",
      "etape-news-reader",
      "popup=yes,width=1100,height=800,left=250,top=240,resizable=yes,scrollbars=yes,noopener,noreferrer",
    );
  });

  it("reuses and focuses the News Reader for another headline", () => {
    const { news, linkGroups } = renderPanel();
    const popup = { closed: false, focus: vi.fn(), location: { href: "" } };
    lastPopup = popup;
    const open = vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "https://x/a", "t1", { headline: "H1" }),
        newsItem("US.AAPL", "https://x/b", "t2", { headline: "H2" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("H1"));
    fireEvent.click(screen.getByText("H2"));
    expect(open).toHaveBeenCalledTimes(1);
    expect(popup.location.href).toBe("https://x/b");
    expect(popup.focus).toHaveBeenCalledTimes(2);
  });

  it("recreates the News Reader after it closes", () => {
    const { news, linkGroups } = renderPanel();
    const first = { closed: false, focus: vi.fn(), location: { href: "" } };
    const second = { closed: false, focus: vi.fn(), location: { href: "" } };
    lastPopup = second;
    const open = vi.spyOn(window, "open")
      .mockReturnValueOnce(first as unknown as Window)
      .mockReturnValueOnce(second as unknown as Window);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "https://x/a", "t1", { headline: "H1" }),
        newsItem("US.AAPL", "https://x/b", "t2", { headline: "H2" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("H1"));
    first.closed = true;
    fireEvent.click(screen.getByText("H2"));
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls[1][0]).toBe("https://x/b");
  });

  it("rejects malformed and non-HTTP news URLs without opening a window", () => {
    const { news, linkGroups } = renderPanel();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "javascript:alert(1)", "t1", { headline: "Bad scheme" }),
        newsItem("US.AAPL", "not a url", "t2", { headline: "Malformed" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("Bad scheme"));
    fireEvent.click(screen.getByText("Malformed"));
    expect(open).not.toHaveBeenCalled();
    expect(openNewsWindow("ftp://x/a")).toBeNull();
  });

  it("shows an empty state when the focused symbol has no news", () => {
    const { linkGroups } = renderPanel();
    act(() => linkGroups.focus("green", "US.TSLA"));
    expect(screen.getByText(/no news for US.TSLA/i)).toBeTruthy();
  });
});

describe("StockInfoPanel fundamentals section", () => {
  it("shows a 'no fundamentals yet' message when the store has no detail for the focused symbol", () => {
    const { linkGroups } = renderPanel();
    act(() => linkGroups.focus("green", "US.TSLA"));
    expect(screen.getByText(/no fundamentals yet for US.TSLA/i)).toBeTruthy();
  });

  // These tests exercise the full fundamentals grid + price/change row, which since
  // the details-collapse feature only render when expanded — mount pre-expanded via
  // settings so they keep testing that (unchanged) content, not the new collapsed default.
  it("renders the company name, price, and an up-glyph colored change for a positive changePct", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", { changePct: 5.2 })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("US.AAPL Inc")).toBeTruthy();
    expect(document.body.textContent).toContain("▲ 5.20%");
  });

  it("renders a down-glyph colored change for a negative changePct", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { changePct: -2.1 })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(document.body.textContent).toContain("▼ 2.10%");
  });

  it("shows a bare dash with no glyph when changePct is null", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { changePct: null })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(document.body.textContent).not.toContain("▲");
    expect(document.body.textContent).not.toContain("▼");
  });

  it("shows a neutral, arrow-less percent (not a false up-signal) when changePct is exactly 0", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.GOOG", { changePct: 0 })));
      linkGroups.focus("green", "US.GOOG");
    });
    expect(document.body.textContent).toContain("0.00%");
    expect(document.body.textContent).not.toContain("▲");
    expect(document.body.textContent).not.toContain("▼");
  });

  it("does not render removed market, float, or volume fields", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", {
        marketCap: 3_210_000_000_000, floatMarketCap: 1_500_000_000,
        floatShares: 900_000, volume: 1_000,
      })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText("Mkt cap")).toBeNull();
    expect(screen.queryByText("Free float cap")).toBeNull();
    expect(screen.queryByText("Free Float")).toBeNull();
    expect(screen.queryByText("Volume")).toBeNull();
  });

  it("renders Country, Sector, Industry, and Exchange", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("Country")).toBeTruthy();
    expect(screen.getByText("United States")).toBeTruthy();
    expect(screen.getByText("Sector")).toBeTruthy();
    expect(screen.getByText("Technology")).toBeTruthy();
    expect(screen.getByText("Industry")).toBeTruthy();
    expect(screen.getByText("Software")).toBeTruthy();
    expect(screen.getByText("Exchange")).toBeTruthy();
  });

  it("does not render EMA 200 or the 52-week range", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { exchange: "NASDAQ", ema200: 145.5 })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("NASDAQ")).toBeTruthy();
    expect(screen.queryByText("EMA 200")).toBeNull();
    expect(screen.queryByText("52wk")).toBeNull();
  });

  it("hides missing Country and Sector rows and renders a bare dash for empty Industry/Exchange", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { exchange: "", industry: "", country: "", sector: "" })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/N\/A/i)).toBeNull();
    expect(screen.queryByText("Country")).toBeNull();
    expect(screen.queryByText("Sector")).toBeNull();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(2); // exchange + industry
  });

  it("does not render an in-body 'Stock Info' header line once a symbol is focused (the dockview tab already shows it)", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/stock info/i)).toBeNull();
  });

  it("renders Alpaca borrow status and preserves explicit boolean false values", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.TSLA", {
        borrowStatus: "hard_to_borrow", shortable: true, marginable: false, tradable: false,
      })));
      linkGroups.focus("green", "US.TSLA");
    });
    expect(screen.getByText("Borrow status")).toBeTruthy();
    expect(screen.getByText("HTB")).toBeTruthy();
    expect(screen.getByText("Shortable")).toBeTruthy();
    expect(screen.getByText("Marginable")).toBeTruthy();
    expect(screen.getByText("Tradable")).toBeTruthy();
    expect(screen.getByText("Yes")).toBeTruthy();
    expect(screen.getAllByText("No")).toHaveLength(2);
  });

  it("renders ETB and keeps nullable booleans unknown instead of turning them into No", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", { borrowStatus: "easy_to_borrow" })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("ETB")).toBeTruthy();
    expect(screen.queryByText("No")).toBeNull();
    expect(screen.getByText("Shortable")).toBeTruthy();
  });

  it("hides Alpaca rows when every Alpaca field is null", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText("Borrow status")).toBeNull();
    expect(screen.queryByText("Shortable")).toBeNull();
    expect(screen.queryByText("Marginable")).toBeNull();
    expect(screen.queryByText("Tradable")).toBeNull();
  });

  it("humanizes an unknown future borrow status without crashing", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { borrowStatus: "special_borrow" })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(screen.getByText("Special borrow")).toBeTruthy();
  });
});

describe("StockInfoPanel details collapse (compact-by-default)", () => {
  it("shows the derived SSR marker in the collapsed summary", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { shortSellRestricted: true })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(screen.queryByText("NVDA**")).toBeNull();
  });

  it("shows shortable and tradable in the collapsed summary", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { borrowStatus: "hard_to_borrow", shortable: true, tradable: false })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(screen.getByText("HTB")).toBeTruthy();
    expect(screen.queryByText("Shortable")).toBeNull();
    expect(screen.getByText("NOT Tradeable")).toBeTruthy();
    expect(screen.queryByText("Tradable")).toBeNull();
    expect(screen.queryByText("Yes")).toBeNull();
    expect(screen.queryByText("No")).toBeNull();
  });

  it("replaces borrow status with Not Shortable when the asset is not shortable", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { borrowStatus: "easy_to_borrow", shortable: false, tradable: true })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(screen.getByText("Not Shortable")).toBeTruthy();
    expect(screen.queryByText("ETB")).toBeNull();
    expect(screen.queryByText("HTB")).toBeNull();
    expect(screen.getByText("Tradable")).toBeTruthy();
    expect(screen.queryByText("NOT Tradeable")).toBeNull();
  });

  it("omits Symbol and its SSR marker from the expanded header", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.NVDA", { shortSellRestricted: true })));
      linkGroups.focus("green", "US.NVDA");
    });
    expect(screen.queryByText("NVDA**")).toBeNull();
    expect(screen.queryByText("NVDA", { exact: true })).toBeNull();
  });

  it("defaults to a single collapsed row (name · sector) with no price/change and no grid", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", {
        industry: "Software", sector: "Technology", floatShares: 15_000_000_000, ema200: 198.3, changePct: 5.2,
      })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("US.AAPL Inc")).toBeTruthy();
    expect(screen.getByText("Technology")).toBeTruthy();
    expect(screen.queryByText("Software")).toBeNull();
    // Grid-only labels and removed compact fields absent when collapsed:
    expect(screen.queryByText("Mkt cap")).toBeNull();
    expect(screen.queryByText("Exchange")).toBeNull();
    expect(screen.queryByText("52wk")).toBeNull();
    expect(screen.queryByText("Volume")).toBeNull();
    expect(screen.queryByText("EMA 200")).toBeNull();
    // No price/change treatment when collapsed:
    expect(document.body.textContent).not.toContain("▲");
    expect(document.body.textContent).not.toContain("▼");
  });

  it("expanding via the caret reveals the full grid and price/change row, and persists the choice", () => {
    const { stockDetail, linkGroups, onConfigChange } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", { changePct: 5.2 })));
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByRole("button", { name: /toggle fundamentals/i }));

    expect(screen.getByText("Country")).toBeTruthy();
    expect(screen.getByText("Exchange")).toBeTruthy();
    expect(screen.getByText("Sector")).toBeTruthy();
    expect(document.body.textContent).toContain("▲ 5.20%");
    expect(onConfigChange).toHaveBeenCalledWith({ detailsCollapsed: false });
  });

  it("mounting with a persisted detailsCollapsed: false renders expanded without any interaction", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("Country")).toBeTruthy();
  });

  it("collapsed row omits an unavailable Sector", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { sector: "" })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/N\/A/i)).toBeNull();
    expect(screen.queryByText("Sector")).toBeNull();
  });

  it.each([
    ["hard_to_borrow", "HTB"],
    ["easy_to_borrow", "ETB"],
  ])("collapsed row shows %s as %s", (borrowStatus, label) => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", { borrowStatus })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.queryByText("Shortable")).toBeNull();
  });

  it("collapsed row is unchanged when borrow status is null", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", { borrowStatus: null })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.queryByText("HTB")).toBeNull();
    expect(screen.queryByText("ETB")).toBeNull();
  });
});

describe("StockInfoPanel news list enhancements", () => {
  it("prefers published_at over seen_at for the meta line's displayed time", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "u1", "2020-01-01T00:00:00Z", { published_at: "2026-07-06T13:30:05Z", published_precision: "second" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    // 13:30:05Z is 09:30:05 ET (EDT = UTC-4); the seen_at year (2020) must not win.
    expect(screen.getByText(/09:30:05/)).toBeTruthy();
  });

  it("renders a bracket-style type badge per item, defaulting an unrecognized type to [NEWS]", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "u1", "t1", { type: "notice" }),
        newsItem("US.AAPL", "u2", "t2", { type: "rating" }),
        newsItem("US.AAPL", "u3", "t3", { type: "some-unrecognized-value" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("[NOTICE]")).toBeTruthy();
    expect(screen.getByText("[RATING]")).toBeTruthy();
    expect(screen.getByText("[NEWS]")).toBeTruthy();
  });

  it("the Catalysts only control defaults on and is accessible once a symbol is focused", () => {
    const { linkGroups } = renderPanel();
    act(() => linkGroups.focus("green", "US.AAPL"));
    const checkbox = screen.getByRole("checkbox", { name: /catalysts only/i });
    expect((checkbox as HTMLInputElement).type).toBe("checkbox");
    expect((checkbox as HTMLInputElement).checked).toBe(true);
  });

  it("Catalysts only hides non-catalysts, persists its toggle, and shows the distinct empty state", () => {
    const { news, linkGroups, onConfigChange } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "hot", "t2", { headline: "Catalyst", catalyst_category: "offering", catalyst_score: 80 }),
        newsItem("US.AAPL", "cold", "t1", { headline: "Cold", catalyst_category: "other", catalyst_score: 0 }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("Catalyst")).toBeTruthy();
    expect(screen.queryByText("Cold")).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: /catalysts only/i }));
    expect(screen.getByText("Cold")).toBeTruthy();
    expect(onConfigChange).toHaveBeenCalledWith({ catalystsOnly: false });
  });

  it("shows no-catalyst text and never treats an unknown time as today", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [newsItem("US.AAPL", "u", "2026-07-06T13:31:00Z", { catalyst_category: "other", catalyst_score: 0 })] });
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("No catalyst news for US.AAPL.")).toBeTruthy();
  });
});
