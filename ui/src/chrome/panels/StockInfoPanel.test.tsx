// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { StockInfoPanel, newsDateLabel } from "./StockInfoPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";
import type { AckMsg, NewsItem, StockDetailPayload, SnapshotMsg } from "../../wire/contract";
import { formatPrice, QUOTE_DECIMALS } from "../../render/format";

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
  const config: PanelConfig = { id: "m-news", panelId: "news", group: "green", settings: opts?.settings ?? {} };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" }), sendQuery: async () => [] } } as PanelProps;
  render(<ThemeProvider><StockInfoPanel {...props} /></ThemeProvider>);
  return { news, stockDetail, linkGroups, onConfigChange };
}

const newsItem = (symbol: string, url: string, seen_at: string, overrides: Partial<NewsItem> = {}): NewsItem =>
  ({ symbol, headline: "h", source: "R", url, seen_at, published_at: "", view_count: 0, type: "news", ...overrides });

const detailPayload = (symbol: string, overrides: Partial<StockDetailPayload> = {}): StockDetailPayload => ({
  symbol, name: `${symbol} Inc`, industry: "Tech", exchange: "NASDAQ", price: 10, lastClose: 9.5, changePct: 5.2,
  marketCap: 3_210_000_000_000, floatMarketCap: 900_000_000, sharesOutstanding: 22_700_000, floatShares: 20_000_000,
  pe: 20, peTTM: 21, eps: 0.5, high52: 15, low52: 5, ema200: 145.5, volume: 1000, refreshedAt: "t1",
  ...overrides,
});
const detailSnap = (p: unknown) => ({ kind: "snapshot", topic: "stock.detail", payload: p } as SnapshotMsg);

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

  it("shows nothing below the header — no Hot only checkbox, no news area — when no symbol is focused", () => {
    renderPanel();
    expect(screen.queryByRole("checkbox", { name: /hot only/i })).toBeNull();
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

  it("clicking a headline opens its url", () => {
    const { news, linkGroups } = renderPanel();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "https://x/a", "t", { headline: "H" })] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("H"));
    expect(open).toHaveBeenCalledWith("https://x/a", "_blank", "noopener,noreferrer");
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

  it("formats market cap, free float cap, free float, and volume with a compact magnitude suffix", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", {
        marketCap: 3_210_000_000_000, floatMarketCap: 1_500_000_000,
        floatShares: 900_000, volume: 1_000,
      })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("3.21T")).toBeTruthy();
    expect(screen.getByText("1.5B")).toBeTruthy();
    expect(screen.getByText("900K")).toBeTruthy();
    expect(screen.getByText("Volume")).toBeTruthy();
    expect(screen.getByText("1K")).toBeTruthy();
  });

  it("renders the 52-week range at QUOTE_DECIMALS", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { low52: 5, high52: 15 })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(document.body.textContent).toContain("5.000–15.000");
  });

  it("renders the renamed Free Float / Free float cap labels, and no longer renders Float, Shares out, P/E · TTM, or EPS", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("Free Float")).toBeTruthy();
    expect(screen.getByText("Free float cap")).toBeTruthy();
    expect(screen.queryByText(/^Float$/)).toBeNull();
    expect(screen.queryByText(/^Float cap$/)).toBeNull();
    expect(screen.queryByText(/shares out/i)).toBeNull();
    expect(screen.queryByText(/P\/E/i)).toBeNull();
    expect(screen.queryByText(/^EPS$/)).toBeNull();
  });

  it("renders Exchange and EMA 200 cells with their values", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { exchange: "NASDAQ", ema200: 145.5 })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("Exchange")).toBeTruthy();
    expect(screen.getByText("NASDAQ")).toBeTruthy();
    expect(screen.getByText("EMA 200")).toBeTruthy();
    expect(document.body.textContent).toContain(formatPrice(145.5, QUOTE_DECIMALS));
  });

  it("renders a bare dash (not N/A) for empty exchange/industry and a null EMA 200", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { exchange: "", industry: "", ema200: null })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/N\/A/i)).toBeNull();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(3); // exchange + industry + ema200
  });

  it("does not render an in-body 'Stock Info' header line once a symbol is focused (the dockview tab already shows it)", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/stock info/i)).toBeNull();
  });
});

describe("StockInfoPanel details collapse (compact-by-default)", () => {
  it("defaults to a single collapsed row (name · industry · Flt · EMA200) with no price/change and no grid", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.AAPL", {
        industry: "Technology", floatShares: 15_000_000_000, ema200: 198.3, changePct: 5.2,
      })));
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("US.AAPL Inc")).toBeTruthy();
    expect(screen.getByText("Technology")).toBeTruthy();
    expect(screen.getByText("15.0B")).toBeTruthy();
    expect(document.body.textContent).toContain(formatPrice(198.3, QUOTE_DECIMALS));
    // Grid-only labels absent when collapsed:
    expect(screen.queryByText("Mkt cap")).toBeNull();
    expect(screen.queryByText("Exchange")).toBeNull();
    expect(screen.queryByText("52wk")).toBeNull();
    expect(screen.queryByText("Volume")).toBeNull();
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

    expect(screen.getByText("Mkt cap")).toBeTruthy();
    expect(screen.getByText("Exchange")).toBeTruthy();
    expect(screen.getByText("Volume")).toBeTruthy();
    expect(document.body.textContent).toContain("▲ 5.20%");
    expect(onConfigChange).toHaveBeenCalledWith({ detailsCollapsed: false });
  });

  it("mounting with a persisted detailsCollapsed: false renders expanded without any interaction", () => {
    const { stockDetail, linkGroups } = renderPanel({ settings: { detailsCollapsed: false } });
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT")));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.getByText("Mkt cap")).toBeTruthy();
  });

  it("collapsed row shows a bare dash (not N/A) for empty industry, null free float, and null EMA 200", () => {
    const { stockDetail, linkGroups } = renderPanel();
    act(() => {
      stockDetail.apply(detailSnap(detailPayload("US.MSFT", { industry: "", floatShares: null, ema200: null })));
      linkGroups.focus("green", "US.MSFT");
    });
    expect(screen.queryByText(/N\/A/i)).toBeNull();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(3); // industry + float + ema200
  });
});

describe("StockInfoPanel news list enhancements", () => {
  it("prefers published_at over seen_at for the meta line's displayed time", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "u1", "2020-01-01T00:00:00Z", { published_at: "2026-07-06T13:30:05Z" }),
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

  it("the Hot only control is a real, accessible checkbox once a symbol is focused", () => {
    const { linkGroups } = renderPanel();
    act(() => linkGroups.focus("green", "US.AAPL"));
    const checkbox = screen.getByRole("checkbox", { name: /hot only/i });
    expect((checkbox as HTMLInputElement).type).toBe("checkbox");
    expect((checkbox as HTMLInputElement).checked).toBe(false);
  });

  it("Hot only filters to type=news items at/above the view-count floor, excluding rating/notice even if hot", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        newsItem("US.AAPL", "hot", "t2", { headline: "Hot news", view_count: 5000, type: "news" }),
        newsItem("US.AAPL", "cold", "t1", { headline: "Cold news", view_count: 10, type: "news" }),
        newsItem("US.AAPL", "hotrating", "t3", { headline: "Hot rating", view_count: 5000, type: "rating" }),
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    expect(screen.getByText("Hot news")).toBeTruthy();
    expect(screen.getByText("Cold news")).toBeTruthy();
    expect(screen.getByText("Hot rating")).toBeTruthy();

    fireEvent.click(screen.getByRole("checkbox", { name: /hot only/i }));

    expect(screen.getByText("Hot news")).toBeTruthy();
    expect(screen.queryByText("Cold news")).toBeNull();
    expect(screen.queryByText("Hot rating")).toBeNull();
  });
});
