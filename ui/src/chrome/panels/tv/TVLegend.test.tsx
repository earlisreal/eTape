// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVLegend.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import type { MutableRefObject } from "react";
import { TVLegend, type TVLegendHandle } from "./TVLegend";
import { getTvChrome } from "../../../render/chart/tvTheme";
import type { IndicatorInstance } from "../../../render/chart/indicatorSeries";

afterEach(cleanup);
const chrome = getTvChrome("light");
const ema: IndicatorInstance = { instanceId: "e1", type: "EMA", params: { period: 9 } };
const macd: IndicatorInstance = { instanceId: "m1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } };

// jsdom/cssstyle normalizes hex assignments to rgb() on readback; run every
// expected color through the same normalization so we're not hardcoding it.
const cssColor = (hex: string): string => {
  const div = document.createElement("div");
  div.style.color = hex;
  return div.style.color;
};

function Harness({ onToggle, hRef, instances = [ema], onClosePane = () => {}, onToggleCollapsePane = () => {} }: {
  onToggle: (id: string) => void; hRef: MutableRefObject<TVLegendHandle | null>;
  instances?: IndicatorInstance[]; onClosePane?: (paneIndex: number) => void; onToggleCollapsePane?: (paneIndex: number) => void;
}) {
  // hRef already has the exact shape TVLegend's legendRef prop expects
  // ({ current: TVLegendHandle | null }), so pass it straight through —
  // no proxy needed to observe what TVLegend assigns to legendRef.current.
  return (
    <TVLegend chrome={chrome} symbol="US.AAPL" timeframe="1m" instances={instances} paneOffsets={[0, 400]} rightAxisWidth={60}
      onToggleHidden={onToggle} onEditIndicator={() => {}} onRemoveIndicator={() => {}}
      onClosePane={onClosePane} onToggleCollapsePane={onToggleCollapsePane}
      legendRef={hRef} />
  );
}

describe("TVLegend", () => {
  it("writes OHLC + indicator values imperatively via the handle", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} />);
    hRef.current!.update({ o: 10, h: 12, l: 9.5, c: 11.5, changePct: 1.2, up: true, volume: 1_240_000,
      indicators: [{ instanceId: "e1", label: "EMA 9 close", paneIndex: 0, values: [11.3], colors: [chrome.accent] }] });
    expect(screen.getByTestId("legend-c").textContent).toContain("11.5");
    expect(screen.getByTestId("legend-vol").textContent).toContain("1.24M");
    expect(screen.getByTestId("legend-ind-e1-0").textContent).toContain("11.3");
  });

  it("reveals hover controls and toggles visibility", () => {
    const onToggle = vi.fn();
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={onToggle} hRef={hRef} />);
    fireEvent.mouseEnter(screen.getByTestId("legend-row-e1"));
    fireEvent.click(screen.getByLabelText("hide e1"));
    expect(onToggle).toHaveBeenCalledWith("e1");
  });

  it("renders a close + collapse control for a sub-pane indicator (MACD) and invokes the handlers with its pane index", () => {
    const onClosePane = vi.fn();
    const onToggleCollapsePane = vi.fn();
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[macd]} onClosePane={onClosePane} onToggleCollapsePane={onToggleCollapsePane} />);
    fireEvent.click(screen.getByLabelText("close pane 1"));
    expect(onClosePane).toHaveBeenCalledWith(1);
    fireEvent.click(screen.getByLabelText("collapse pane 1"));
    expect(onToggleCollapsePane).toHaveBeenCalledWith(1);
  });

  it("shows an 'expand' label once the pane's indicator is marked collapsed", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[{ ...macd, collapsed: true }]} />);
    expect(screen.getByLabelText("expand pane 1")).toBeTruthy();
  });

  it("renders no pane controls for a main-pane-only instance", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[ema]} />);
    expect(screen.queryByLabelText(/close pane|collapse pane|expand pane/)).toBeNull();
  });

  it("writes an OPEN/CLOSE signal badge for a MACD row, tinted up/down", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[macd]} />);
    hRef.current!.update({ o: 10, h: 12, l: 9.5, c: 11.5, changePct: 1.2, up: true, volume: null,
      indicators: [{ instanceId: "m1", label: "MACD 12 26 9", paneIndex: 1, values: [0.5, 0.3, 0.2],
        colors: [chrome.accent, chrome.accent, chrome.accent], signal: "open" }] });
    const badge = screen.getByTestId("legend-sig-m1");
    expect(badge.textContent).toBe("OPEN");
    // jsdom normalizes a hex color style to rgb(...); build the expected value the
    // same way the DOM would, rather than comparing raw hex to normalized rgb.
    const probe = document.createElement("span");
    probe.style.color = chrome.up;
    expect(badge.style.color).toBe(probe.style.color);
  });

  it("does not render a signal badge cell for a non-MACD indicator", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[ema]} />);
    expect(screen.queryByTestId("legend-sig-e1")).toBeNull();
  });

  it("hovering a legend control button shows the chrome.hover/chrome.text overlay", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} />);
    fireEvent.mouseEnter(screen.getByTestId("legend-row-e1"));
    const hideBtn = screen.getByLabelText("hide e1") as HTMLButtonElement;
    const gearBtn = screen.getByLabelText("settings e1") as HTMLButtonElement;
    const closeBtn = screen.getByLabelText("remove e1") as HTMLButtonElement;

    expect(hideBtn.style.background).toBe("transparent");

    // Note: don't fireEvent.mouseLeave the button here — with no relatedTarget,
    // RTL's mouseleave is treated as leaving the whole subtree, which also
    // fires the row's own onMouseLeave and unmounts these buttons (existing
    // row-level reveal behavior, out of scope for this button-level check).
    for (const btn of [hideBtn, gearBtn, closeBtn]) {
      fireEvent.mouseEnter(btn);
      expect(btn.style.background).toBe(cssColor(chrome.hover));
      expect(btn.style.color).toBe(cssColor(chrome.text));
    }
  });

  it("hovering a pane collapse/close control shows the same chrome.hover/chrome.text overlay", () => {
    const hRef: { current: TVLegendHandle | null } = { current: null };
    render(<Harness onToggle={() => {}} hRef={hRef} instances={[macd]} />);
    const collapseBtn = screen.getByLabelText("collapse pane 1") as HTMLButtonElement;
    const closeBtn = screen.getByLabelText("close pane 1") as HTMLButtonElement;

    for (const btn of [collapseBtn, closeBtn]) {
      fireEvent.mouseEnter(btn);
      expect(btn.style.background).toBe(cssColor(chrome.hover));
      expect(btn.style.color).toBe(cssColor(chrome.text));
      fireEvent.mouseLeave(btn);
      expect(btn.style.background).toBe("transparent");
    }
  });
});
