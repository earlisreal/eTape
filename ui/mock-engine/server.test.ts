import { describe, it, expect, afterEach } from "vitest";
import { WebSocket } from "ws";
import { startMockEngine, type Fixture } from "./server";

const PORT = 8699;
let handle: { close: () => Promise<void> } | null = null;
afterEach(async () => { await handle?.close(); handle = null; });

const fixture: Fixture = {
  snapshots: [{ topic: "md.quote", key: "US.AAPL", payload: { symbol: "US.AAPL", last: 3.5 } }],
  deltas: [{ afterMs: 10, topic: "md.quote", key: "US.AAPL", payload: { last: 3.6 } }],
};

interface WireMsg {
  kind: string;
  topic?: string;
  key?: string;
  payload?: { symbol?: string; last?: number };
  corrId?: string;
  status?: string;
}

function collect(ws: WebSocket, n: number, timeoutMs = 1000): Promise<WireMsg[]> {
  return new Promise((resolve, reject) => {
    const out: WireMsg[] = [];
    const timer = setTimeout(() => reject(new Error(`only got ${out.length}/${n}`)), timeoutMs);
    ws.on("message", (d) => {
      out.push(JSON.parse(d.toString()));
      if (out.length === n) { clearTimeout(timer); resolve(out); }
    });
  });
}

describe("mock engine", () => {
  it("sends snapshot then delta for a subscribed topic", async () => {
    handle = startMockEngine({ port: PORT, fixture });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise<void>((r) => ws.on("open", () => r()));
    const msgs = collect(ws, 2);
    ws.send(JSON.stringify({ kind: "subscribe", topic: "md.quote" }));
    const [snap, delta] = await msgs;
    expect(snap.kind).toBe("snapshot");
    expect(delta.kind).toBe("delta");
    expect(delta.payload?.last).toBe(3.6);
    ws.close();
  });

  it("acks a command", async () => {
    handle = startMockEngine({ port: PORT, fixture });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise<void>((r) => ws.on("open", () => r()));
    const msgs = collect(ws, 1);
    ws.send(JSON.stringify({ kind: "command", corrId: "c1", name: "Noop", args: {} }));
    const [ack] = await msgs;
    expect(ack).toMatchObject({ kind: "ack", corrId: "c1", status: "accepted" });
    ws.close();
  });
});
