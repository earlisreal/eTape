import { afterEach, describe, expect, it, vi } from "vitest";
import type { LinkGroup } from "./linkGroups";
import { CrosshairSync, type CrosshairCursor } from "./crosshairSync";

class ChannelHub {
  readonly channels: TestChannel[] = [];

  open = (): BroadcastChannel => {
    const channel = new TestChannel(this);
    this.channels.push(channel);
    return channel as unknown as BroadcastChannel;
  };
}

class TestChannel {
  onmessage: ((event: MessageEvent) => void) | null = null;
  connected = true;
  private readonly listeners = new Set<(event: MessageEvent) => void>();

  constructor(private readonly hub: ChannelHub) {}

  addEventListener(_type: string, listener: (event: MessageEvent) => void): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: string, listener: (event: MessageEvent) => void): void {
    this.listeners.delete(listener);
  }

  postMessage(data: unknown): void {
    if (!this.connected) return;
    const event = { data } as MessageEvent;
    for (const peer of this.hub.channels) {
      if (peer === this || !peer.connected) continue;
      peer.onmessage?.(event);
      for (const listener of peer.listeners) listener(event);
    }
  }

  close(): void { this.connected = false; }
}

const cursor = (group: Exclude<LinkGroup, null>, symbol = "US.AAPL"): CrosshairCursor => ({
  group, symbol, timeframe: "1m", timeMs: Date.parse("2026-10-01T13:32:00Z"), price: 215.5,
});

afterEach(() => { vi.useRealTimers(); });

describe("CrosshairSync", () => {
  it("delivers a cursor only to enabled consumers with the same Link Group and symbol", () => {
    const hub = new ChannelHub();
    const source = new CrosshairSync(hub.open);
    const same = new CrosshairSync(hub.open);
    const otherGroup = new CrosshairSync(hub.open);
    const otherSymbol = new CrosshairSync(hub.open);
    const disabled = new CrosshairSync(hub.open);
    const ownCursor = vi.fn();
    const sameCursor = vi.fn();
    const otherGroupCursor = vi.fn();
    const otherSymbolCursor = vi.fn();
    const disabledCursor = vi.fn();
    const sourceHandle = source.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), ownCursor);
    const sameHandle = same.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), sameCursor);
    const groupHandle = otherGroup.register(() => ({ group: "blue", symbol: "US.AAPL", enabled: true }), otherGroupCursor);
    const symbolHandle = otherSymbol.register(() => ({ group: "green", symbol: "US.MSFT", enabled: true }), otherSymbolCursor);
    const disabledHandle = disabled.register(() => ({ group: "green", symbol: "US.AAPL", enabled: false }), disabledCursor);

    ownCursor.mockClear(); sameCursor.mockClear(); otherGroupCursor.mockClear(); otherSymbolCursor.mockClear(); disabledCursor.mockClear();
    sourceHandle.publish(cursor("green"));

    expect(ownCursor).not.toHaveBeenCalled();
    expect(sameCursor).toHaveBeenLastCalledWith(expect.objectContaining({ symbol: "US.AAPL" }));
    expect(otherGroupCursor).not.toHaveBeenCalled();
    expect(otherSymbolCursor).not.toHaveBeenCalled();
    expect(disabledCursor).not.toHaveBeenCalled();
    sourceHandle.dispose(); sameHandle.dispose(); groupHandle.dispose(); symbolHandle.dispose(); disabledHandle.dispose();
  });

  it("keeps the newest source when an older owner's clear arrives later", () => {
    vi.useFakeTimers();
    const hub = new ChannelHub();
    const first = new CrosshairSync(hub.open);
    const second = new CrosshairSync(hub.open);
    const receiver = new CrosshairSync(hub.open);
    const received = vi.fn();
    const firstHandle = first.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), vi.fn());
    const secondHandle = second.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), vi.fn());
    const receiverHandle = receiver.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), received);

    firstHandle.publish(cursor("green"));
    vi.advanceTimersByTime(1);
    secondHandle.publish(cursor("green"));
    firstHandle.clear();

    expect(received).toHaveBeenLastCalledWith(expect.objectContaining({ symbol: "US.AAPL" }));
    secondHandle.clear();
    expect(received).toHaveBeenLastCalledWith(null);
    firstHandle.dispose(); secondHandle.dispose(); receiverHandle.dispose();
  });

  it("replays a stationary cursor to a receiver that joins later", () => {
    vi.useFakeTimers();
    const hub = new ChannelHub();
    const source = new CrosshairSync(hub.open);
    const receiver = new CrosshairSync(hub.open);
    const received = vi.fn();
    const sourceHandle = source.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), vi.fn());
    sourceHandle.publish(cursor("green"));
    const receiverHandle = receiver.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), received);

    vi.advanceTimersByTime(1_000);

    expect(received).toHaveBeenLastCalledWith(expect.objectContaining({ symbol: "US.AAPL" }));
    sourceHandle.dispose(); receiverHandle.dispose();
  });

  it("expires a cursor when its source disappears without a clear", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-02T00:00:00Z"));
    const hub = new ChannelHub();
    const source = new CrosshairSync(hub.open);
    const receiver = new CrosshairSync(hub.open);
    const received = vi.fn();
    const sourceHandle = source.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), vi.fn());
    const receiverHandle = receiver.register(() => ({ group: "green", symbol: "US.AAPL", enabled: true }), received);

    sourceHandle.publish(cursor("green"));
    hub.channels[0].connected = false;
    vi.advanceTimersByTime(4_000);

    expect(received).toHaveBeenLastCalledWith(null);
    sourceHandle.dispose(); receiverHandle.dispose();
  });
});
