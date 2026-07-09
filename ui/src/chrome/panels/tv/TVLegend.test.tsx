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
});
