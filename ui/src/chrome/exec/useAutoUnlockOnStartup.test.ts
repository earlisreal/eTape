// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useAutoUnlockOnStartup } from "./useAutoUnlockOnStartup";

describe("useAutoUnlockOnStartup", () => {
  it("calls onUnlock exactly once when enabled, ready from the first render, and not armed", () => {
    const onUnlock = vi.fn();
    renderHook(() => useAutoUnlockOnStartup({ ready: true, enabled: true, armed: false, onUnlock }));
    expect(onUnlock).toHaveBeenCalledTimes(1);
  });

  it("never calls onUnlock when disabled, regardless of ready/armed", () => {
    const onUnlock = vi.fn();
    const { rerender } = renderHook(
      (p: { ready: boolean; armed: boolean }) => useAutoUnlockOnStartup({ ready: p.ready, enabled: false, armed: p.armed, onUnlock }),
      { initialProps: { ready: false, armed: false } },
    );
    rerender({ ready: true, armed: false });
    rerender({ ready: true, armed: true });
    expect(onUnlock).not.toHaveBeenCalled();
  });

  it("never calls onUnlock when already armed at the moment ready becomes true", () => {
    const onUnlock = vi.fn();
    renderHook(() => useAutoUnlockOnStartup({ ready: true, enabled: true, armed: true, onUnlock }));
    expect(onUnlock).not.toHaveBeenCalled();
  });

  it("latches after the initial ready check — later armed/enabled changes never trigger a second call", () => {
    const onUnlock = vi.fn();
    const { rerender } = renderHook(
      (p: { enabled: boolean; armed: boolean }) => useAutoUnlockOnStartup({ ready: true, enabled: p.enabled, armed: p.armed, onUnlock }),
      { initialProps: { enabled: true, armed: false } },
    );
    expect(onUnlock).toHaveBeenCalledTimes(1);

    // Manual lock, then a later reconnect/re-arm from elsewhere, then unlock
    // again — none of this should ever re-fire onUnlock once the latch is set.
    rerender({ enabled: true, armed: true });
    rerender({ enabled: true, armed: false });
    rerender({ enabled: false, armed: false });
    rerender({ enabled: false, armed: true });

    expect(onUnlock).toHaveBeenCalledTimes(1);
  });
});
