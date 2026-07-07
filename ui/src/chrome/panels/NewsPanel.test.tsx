// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { NewsPanel, newsDateLabel } from "./NewsPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";
import type { AckMsg } from "../../wire/contract";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel() {
  const stores = makeStores();
  const news = stores.news;
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  const config: PanelConfig = { id: "m-news", panelId: "news", group: "green", settings: {} };
  const props = { config, stores, linkGroups, onConfigChange: vi.fn(), scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" }), sendQuery: async () => [] } } as PanelProps;
  render(<ThemeProvider><NewsPanel {...props} /></ThemeProvider>);
  return { news, linkGroups };
}

describe("newsDateLabel", () => {
  it("labels today vs older dates", () => {
    const now = Date.parse("2026-07-07T12:00:00Z");
    expect(newsDateLabel("2026-07-07T09:00:00Z", now)).toEqual({ label: "today", today: true });
    expect(newsDateLabel("2026-07-04T08:00:00Z", now).today).toBe(false);
    expect(newsDateLabel("2026-07-04T08:00:00Z", now).label).toMatch(/Jul\s*4/);
  });
});

describe("NewsPanel", () => {
  it("shows a reserved halt-banner slot and a no-symbol header before focus", () => {
    renderPanel();
    expect(screen.getByTestId("halt-slot")).toBeTruthy();
    expect(screen.getByText(/no symbol focused/i)).toBeTruthy();
  });

  it("follows the group's focused symbol and lists its news newest-first", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        { symbol: "US.AAPL", headline: "Older AAPL", source: "R", url: "u1", seen_at: "2026-07-06T13:28:00Z" },
        { symbol: "US.AAPL", headline: "Newer AAPL", source: "R", url: "u2", seen_at: "2026-07-06T13:31:00Z" },
        { symbol: "US.NVDA", headline: "NVDA news", source: "R", url: "n1", seen_at: "2026-07-06T13:30:00Z" },
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    const links = screen.getAllByRole("link");
    expect(links.map((a) => a.textContent)).toEqual(["Newer AAPL", "Older AAPL"]); // newest first, NVDA excluded
    // meta line is date · seen-time · source, in mono, one per row
    expect(document.querySelectorAll(".mono").length).toBe(2);
    expect(screen.getAllByText(/\d{2}:\d{2}:\d{2}/).length).toBeGreaterThan(0);
  });

  it("clicking a headline opens its url", () => {
    const { news, linkGroups } = renderPanel();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        { symbol: "US.AAPL", headline: "H", source: "R", url: "https://x/a", seen_at: "t" }] });
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
