import { WebSocketServer, type WebSocket } from "ws";

export interface Fixture {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }>;
  reconnectAtMs?: number;
}

export function startMockEngine(opts: {
  port: number;
  fixture: Fixture;
  onCommand?: (msg: { kind: "command"; corrId?: string; name?: string; args?: unknown }, send: (m: unknown, afterMs?: number) => void) => void;
  onQuery?: (msg: { kind: "query"; corrId?: string; name?: string; args?: unknown }, send: (m: unknown, afterMs?: number) => void) => void;
}): { close: () => Promise<void> } {
  const wss = new WebSocketServer({ port: opts.port, path: "/ws" });
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const track = (fn: () => void, ms: number) => {
    const t = setTimeout(() => { timers.delete(t); fn(); }, ms);
    timers.add(t);
  };

  wss.on("connection", (ws: WebSocket) => {
    const live = new Set<string>();
    let dropped = false;

    const send = (m: unknown, afterMs = 0) => {
      if (afterMs <= 0) { if (ws.readyState === ws.OPEN) ws.send(JSON.stringify(m)); return; }
      track(() => { if (!dropped && ws.readyState === ws.OPEN) ws.send(JSON.stringify(m)); }, afterMs);
    };

    ws.on("message", (raw) => {
      let msg: { kind?: string; topic?: string; corrId?: string; name?: string; args?: unknown; t?: number };
      try { msg = JSON.parse(raw.toString()); } catch { return; }

      if (msg.kind === "ping") { ws.send(JSON.stringify({ kind: "pong", t: msg.t })); return; }
      if (msg.kind === "query") {
        if (opts.onQuery) opts.onQuery(msg as never, send);
        else send({ kind: "result", corrId: msg.corrId, payload: [] }); // default: empty result, never a dangling promise
        return;
      }
      if (msg.kind === "command") {
        if (opts.onCommand) opts.onCommand(msg as never, send);
        else ws.send(JSON.stringify({ kind: "ack", corrId: msg.corrId, status: "accepted" }));
        return;
      }
      if (msg.kind === "unsubscribe" && msg.topic) { live.delete(msg.topic); return; }
      if (msg.kind === "subscribe" && msg.topic) {
        live.add(msg.topic);
        for (const s of opts.fixture.snapshots.filter((s) => s.topic === msg.topic)) {
          ws.send(JSON.stringify({ kind: "snapshot", topic: s.topic, key: s.key, payload: s.payload }));
        }
        for (const d of opts.fixture.deltas.filter((d) => d.topic === msg.topic)) {
          track(() => {
            if (!dropped && live.has(d.topic) && ws.readyState === ws.OPEN) {
              ws.send(JSON.stringify({ kind: "delta", topic: d.topic, key: d.key, payload: d.payload }));
            }
          }, d.afterMs);
        }
      }
    });

    if (opts.fixture.reconnectAtMs !== undefined) {
      track(() => { dropped = true; ws.close(); }, opts.fixture.reconnectAtMs);
    }
  });

  return {
    close: () =>
      new Promise<void>((resolve) => {
        timers.forEach(clearTimeout);
        timers.clear();
        wss.clients.forEach((c) => c.terminate());
        wss.close(() => resolve());
      }),
  };
}
