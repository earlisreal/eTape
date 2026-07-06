// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useThrottledQuote } from "./useThrottledQuote";
import { QuoteStore } from "../../data/QuoteStore";

describe("useThrottledQuote", () => {
  it("reads the current quote on mount and refreshes on the throttle interval", () => {
    vi.useFakeTimers();
    const qs = new QuoteStore();
    qs.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 1, ask: 2, last: 1.5, ts: "" } });
    const { result } = renderHook(() => useThrottledQuote(qs, "US.AAPL", 10));
    expect(result.current?.ask).toBe(2);
    qs.apply({ kind: "delta", topic: "md.quote" as never, payload: { symbol: "US.AAPL", ask: 3 } });
    act(() => vi.advanceTimersByTime(120)); // > 1000/10ms
    expect(result.current?.ask).toBe(3);
    vi.useRealTimers();
  });
});
