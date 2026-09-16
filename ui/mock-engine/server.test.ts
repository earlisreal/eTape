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

  it("routes commands through onCommand (orderId ack + emitted event)", async () => {
    handle = startMockEngine({
      port: PORT,
      fixture: { snapshots: [], deltas: [] },
      onCommand: (msg, send) => {
        send({ kind: "ack", corrId: msg.corrId, status: "accepted", orderId: "ET-mock-1" });
        send({ kind: "delta", topic: "exec.orders", key: "ET-mock-1", payload: { id: "ET-mock-1", status: "SUBMITTED" } }, 5);
      },
    });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise((r) => ws.on("open", r));
    const got = collect(ws, 2);
    ws.send(JSON.stringify({ kind: "command", corrId: "c1", name: "SubmitOrder", args: {} }));
    const msgs = await got;
    expect(msgs[0]).toMatchObject({ kind: "ack", corrId: "c1", orderId: "ET-mock-1" });
    expect(msgs[1]).toMatchObject({ kind: "delta", topic: "exec.orders", key: "ET-mock-1" });
  });

  it("answers a query via onQuery with a correlated result", async () => {
    handle = startMockEngine({
      port: PORT,
      fixture: { snapshots: [], deltas: [] },
      onQuery: (msg, send) => send({ kind: "result", corrId: msg.corrId, payload: [] }),
    });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise((r) => ws.on("open", r));
    const got = collect(ws, 1);
    ws.send(JSON.stringify({ kind: "query", corrId: "q1", name: "QueryFills", args: {} }));
    expect((await got)[0]).toMatchObject({ kind: "result", corrId: "q1" });
  });

  it("defaults an unhandled query to an empty result (no dangling promise)", async () => {
    handle = startMockEngine({ port: PORT, fixture: { snapshots: [], deltas: [] } }); // no onQuery
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise((r) => ws.on("open", r));
    const got = collect(ws, 1);
    ws.send(JSON.stringify({ kind: "query", corrId: "q9", name: "QueryFills", args: {} }));
    expect((await got)[0]).toMatchObject({ kind: "result", corrId: "q9", payload: [] });
  });
});
