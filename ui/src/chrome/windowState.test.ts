import { afterEach, describe, expect, it, vi } from "vitest";
import { trackWindowState, type WindowStateWindow } from "./windowState";

class FakeWindow implements WindowStateWindow {
  screenX = 10;
  screenY = 20;
  outerWidth = 1200;
  outerHeight = 900;
  private readonly listeners = new Set<() => void>();

  addEventListener(_type: "resize", listener: () => void): void { this.listeners.add(listener); }
  removeEventListener(_type: "resize", listener: () => void): void { this.listeners.delete(listener); }
  resize(): void { this.listeners.forEach((listener) => listener()); }
}

describe("trackWindowState", () => {
  afterEach(() => vi.useRealTimers());

  it("registers initial bounds, coalesces resize, and polls position changes", () => {
    vi.useFakeTimers();
    const win = new FakeWindow();
    let state: ((value: string) => void) | undefined;
    const client = {
      sendCommand: vi.fn(async () => ({ status: "accepted" })),
      onState: (cb: (value: string) => void) => { state = cb; cb("connecting"); return () => { state = undefined; }; },
    };
    const stop = trackWindowState("monitoring", client, win);

    expect(client.sendCommand).toHaveBeenCalledWith("SetWindowState", {
      workspaceId: "monitoring", x: 10, y: 20, width: 1200, height: 900,
    });

    win.outerWidth = 1300;
    win.resize();
    vi.advanceTimersByTime(149);
    expect(client.sendCommand).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    expect(client.sendCommand).toHaveBeenCalledTimes(2);

    win.screenX = -1920;
    vi.advanceTimersByTime(1000);
    expect(client.sendCommand).toHaveBeenLastCalledWith("SetWindowState", expect.objectContaining({ x: -1920, width: 1300 }));

    state?.("open");
    expect(client.sendCommand).toHaveBeenCalledTimes(3); // first open flushes the initial buffered command
    state?.("open");
    expect(client.sendCommand).toHaveBeenCalledTimes(4); // reconnect re-registers the same bounds
    stop();
    win.screenX = 0;
    win.resize();
    vi.advanceTimersByTime(2000);
    expect(client.sendCommand).toHaveBeenCalledTimes(4);
  });
});
