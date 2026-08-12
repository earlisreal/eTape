import { afterEach, describe, expect, it } from "vitest";
import {
  HotkeyTargetCoordinator,
  type HotkeyTargetChannel,
  type HotkeyTargetMessage,
} from "./hotkeyTarget";

class ChannelHub {
  private readonly channels = new Set<FakeChannel>();

  channel(): HotkeyTargetChannel {
    const channel = new FakeChannel(this);
    this.channels.add(channel);
    return channel;
  }

  broadcast(sender: FakeChannel | null, message: HotkeyTargetMessage): void {
    for (const channel of this.channels) if (channel !== sender) channel.receive(message);
  }

  remove(channel: FakeChannel): void { this.channels.delete(channel); }
}

class FakeChannel implements HotkeyTargetChannel {
  private listener: ((event: MessageEvent<HotkeyTargetMessage>) => void) | undefined;

  constructor(private readonly hub: ChannelHub) {}

  postMessage(message: HotkeyTargetMessage): void { this.hub.broadcast(this, message); }
  addEventListener(_type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void { this.listener = listener; }
  removeEventListener(_type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void {
    if (this.listener === listener) this.listener = undefined;
  }
  close(): void { this.hub.remove(this); }
  receive(message: HotkeyTargetMessage): void { this.listener?.({ data: message } as MessageEvent<HotkeyTargetMessage>); }
}

const target = { panel: "chart-a", group: "blue" as const, symbol: "US.AAPL", venue: "sim-paper" };

describe("HotkeyTargetCoordinator", () => {
  const open: HotkeyTargetCoordinator[] = [];
  afterEach(() => { for (const coordinator of open.splice(0)) coordinator.close(); });

  const coordinator = (hub: ChannelHub, ownerWindow: string): HotkeyTargetCoordinator => {
    const value = new HotkeyTargetCoordinator(ownerWindow, hub.channel());
    open.push(value);
    return value;
  };

  it("replays the latest target to a window opened later", () => {
    const hub = new ChannelHub();
    const first = coordinator(hub, "window-a");
    first.activate(target);

    const late = coordinator(hub, "window-b");

    expect(late.snapshot()).toEqual(first.snapshot());
  });

  it("rejects stale revisions and preserves an ungrouped target as a real snapshot", () => {
    const hub = new ChannelHub();
    const owner = coordinator(hub, "window-a");
    const observer = coordinator(hub, "window-b");
    owner.activate(target);
    owner.updateOwned(target.panel, { group: null, symbol: "US.MSFT", venue: "sim-paper" });
    const current = observer.snapshot();
    expect(current?.group).toBeNull();

    hub.broadcast(null, {
      type: "target",
      target: { ownerWindow: "window-a", ...target, revision: (current?.revision ?? 1) - 1 },
    });

    expect(observer.snapshot()).toEqual(current);
  });

  it("updates the owning panel context without allowing another window to clear it", () => {
    const hub = new ChannelHub();
    const owner = coordinator(hub, "window-a");
    const other = coordinator(hub, "window-b");
    owner.activate(target);

    expect(owner.updateOwned(target.panel, { group: "green", symbol: "US.NVDA", venue: "tradezero" })).toBe(true);
    expect(other.snapshot()).toMatchObject({ ownerWindow: "window-a", panel: target.panel, group: "green", symbol: "US.NVDA", venue: "tradezero" });
    expect(other.clearOwned(target.panel)).toBe(false);
    expect(owner.snapshot()).not.toBeNull();

    expect(owner.clearOwned(target.panel)).toBe(true);
    expect(owner.snapshot()).toBeNull();
    expect(other.snapshot()).toBeNull();
  });

  it("keeps a cleared owner's tombstone when another window has already won", () => {
    const hub = new ChannelHub();
    const owner = coordinator(hub, "window-a");
    const other = coordinator(hub, "window-b");
    owner.activate(target);
    other.activate({ ...target, panel: "chart-b" });

    expect(owner.clearOwned(target.panel)).toBe(false);
    hub.broadcast(null, { type: "clear", ownerWindow: "window-a", panel: target.panel, revision: 3 });
    expect(other.snapshot()?.panel).toBe("chart-b");
    hub.broadcast(null, { type: "target", target: { ownerWindow: "window-a", ...target, revision: 1 } });
    expect(other.snapshot()?.panel).toBe("chart-b");
  });
});
