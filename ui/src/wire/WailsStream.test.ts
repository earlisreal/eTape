import { beforeEach, describe, expect, it, vi } from "vitest";
import { WsClient } from "./WsClient";
import { makeWailsSocketFactory, type WailsStreamLike } from "./WailsStream";

class FakeWailsStream implements WailsStreamLike {
  binaryType = "arraybuffer";
  sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onclose: ((event: { code?: number; reason?: string }) => void) | null = null;

  send(data: string): void { this.sent.push(data); }
  close(): void { this.closed = true; }
  open(): void { this.onopen?.(); }
  emit(data: unknown): void { this.onmessage?.({ data }); }
  drop(code = 1006, reason = "network"): void { this.onclose?.({ code, reason }); }
}

describe("Wails stream adapter", () => {
  let streams: FakeWailsStream[];

  beforeEach(() => { streams = []; });

  it("holds WsClient open until the application handshake, then preserves snapshot delivery across reconnect", async () => {
    let sessionNumber = 0;
    const openSession = vi.fn(async () => `opaque-${++sessionNumber}`);
    const client = new WsClient({
      url: "wails://etape.runtime",
      socketFactory: makeWailsSocketFactory("main", {
        openSession,
        streamFactory: () => {
          const stream = new FakeWailsStream();
          streams.push(stream);
          return stream;
        },
      }),
      now: () => 1000,
      setTimeout,
      backoff: () => 0,
    });
    const states: string[] = [];
    client.onState((state) => states.push(state));
    const snapshots: unknown[] = [];
    const dispose = client.subscribe("sys.session", (message) => snapshots.push(message.payload));

    client.start();
    await Promise.resolve();
    expect(streams).toHaveLength(1);
    streams[0].open();
    expect(JSON.parse(streams[0].sent[0])).toEqual({
      protocol: 1,
      workspaceId: "main",
      session: "opaque-1",
    });
    expect(states).toEqual(["connecting"]);

    streams[0].emit(JSON.stringify({ type: "accepted" }));
    expect(states).toContain("open");
    expect(JSON.parse(streams[0].sent[1])).toEqual({ kind: "subscribe", topic: "sys.session" });
    streams[0].emit(JSON.stringify({ kind: "snapshot", topic: "sys.session", payload: { mode: "demo" } }));
    expect(snapshots).toEqual([{ mode: "demo" }]);

    streams[0].drop();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await Promise.resolve();
    expect(streams).toHaveLength(2);
    streams[1].open();
    expect(JSON.parse(streams[1].sent[0]).session).toBe("opaque-2");
    expect(states).toContain("reconnecting");
    expect(states.at(-1)).toBe("connecting");
    streams[1].emit(JSON.stringify({ type: "accepted" }));
    expect(states.at(-1)).toBe("open");
    expect(JSON.parse(streams[1].sent[1])).toEqual({ kind: "subscribe", topic: "sys.session" });
    streams[1].emit(JSON.stringify({ type: "stopping", reason: "engine stopped" }));
    expect(states.at(-1)).toBe("stopped");

    dispose();
    client.stop();
    expect(openSession).toHaveBeenCalledTimes(2);
  });
});
