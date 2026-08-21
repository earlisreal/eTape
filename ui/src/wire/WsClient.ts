import type {
  AckMsg, ClientMessage, DeltaMsg, ServerMessage, SnapshotMsg, TopicName,
} from "./contract";
import { decodeServerMessage, encodeClientMessage } from "./codec";
import { perf } from "../perf/PerfMonitor";
import { uiLog } from "../logging/logger";
import type { MarketClockUpdate } from "../data/MarketClock";

export interface ISocket {
  send(data: string): void;
  close(): void;
  onopen: (() => void) | null;
  onmessage: ((data: string) => void) | null;
  onclose: ((event?: SocketCloseEvent) => void) | null;
}
export interface SocketCloseEvent { code: number; reason: string }
export type SetTimeoutLike = (fn: () => void, ms: number) => unknown;
export type ConnState = "connecting" | "open" | "reconnecting" | "stopped";
type TopicHandler = (m: SnapshotMsg | DeltaMsg) => void;
interface PendingCommand { command: string; resolve: (ack: AckMsg) => void; sent: boolean }
interface PendingQuery { resolve: (payload: unknown) => void; reject: (reason: unknown) => void; sent: boolean }
interface OutboxMessage { raw: string; corrId: string }

interface Opts {
  url: string;
  socketFactory: (url: string) => ISocket;
  now: () => number;
  setTimeout: SetTimeoutLike;
  backoff?: (attempt: number) => number;
  onMarketClockSample?: (sample: MarketClockUpdate) => void;
}

const DEFAULT_BACKOFF = (attempt: number) => {
  const base = Math.min(30_000, 1000 * 2 ** attempt);
  return base / 2 + Math.random() * (base / 2); // jittered 1s → 30s
};

export class WsClient {
  private socket: ISocket | null = null;
  private state: ConnState = "connecting";
  private attempt = 0;
  private corr = 0;
  private lastRtt: number | null = null;
  private readonly handlers = new Map<TopicName, Set<TopicHandler>>();
  private readonly stateCbs = new Set<(s: ConnState) => void>();
  private readonly pending = new Map<string, PendingCommand>();
  private readonly pendingQueries = new Map<string, PendingQuery>();
  private readonly outbox: OutboxMessage[] = []; // requests buffered while not open
  private readonly backoff: (attempt: number) => number;
  private malformedFrames = 0;
  private terminal = false;

  constructor(private readonly opts: Opts) {
    this.backoff = opts.backoff ?? DEFAULT_BACKOFF;
  }

  start(): void { this.terminal = false; this.connect(); }
  stop(): void { this.terminal = true; this.socket?.close(); this.socket = null; }

  onState(cb: (s: ConnState) => void): void { this.stateCbs.add(cb); cb(this.state); }
  rttMs(): number | null { return this.lastRtt; }

  subscribe(topic: TopicName, onMessage: TopicHandler): () => void {
    let set = this.handlers.get(topic);
    if (!set) {
      set = new Set();
      this.handlers.set(topic, set);
      this.sendRaw({ kind: "subscribe", topic }); // first subscriber
    }
    set.add(onMessage);
    return () => {
      const s = this.handlers.get(topic);
      if (!s) return;
      s.delete(onMessage);
      if (s.size === 0) {
        this.handlers.delete(topic);
        this.sendRaw({ kind: "unsubscribe", topic }); // last unsubscriber
      }
    };
  }

  sendCommand(name: string, args: unknown): Promise<AckMsg> {
    const corrId = `c${++this.corr}`;
    return new Promise<AckMsg>((resolve) => {
      const pending = { command: name, resolve, sent: false };
      this.pending.set(corrId, pending);
      pending.sent = this.sendRaw({ kind: "command", corrId, name, args });
    });
  }

  sendQuery(name: string, args: unknown): Promise<unknown> {
    const corrId = `q${++this.corr}`;
    return new Promise<unknown>((resolve, reject) => {
      const pending = { resolve, reject, sent: false };
      this.pendingQueries.set(corrId, pending);
      pending.sent = this.sendRaw({ kind: "query", corrId, name, args });
    });
  }

  sendPing(): void { this.sendRaw({ kind: "ping", t: this.opts.now() }); }

  // ---- internals ----
  private setState(s: ConnState): void {
    if (s === this.state) return; // dedupe consecutive identical states
    this.state = s;
    this.stateCbs.forEach((cb) => cb(s));
  }

  private connect(): void {
    if (this.terminal) return;
    this.setState("connecting");
    const sock = this.opts.socketFactory(this.opts.url);
    this.socket = sock;
    sock.onopen = () => {
      const reconnect = this.attempt > 0;
      const bufferedCommands = this.outbox.length;
      this.attempt = 0;
      this.setState("open");
      uiLog.info("ws connected", {
        reconnect,
        bufferedCommands,
        pendingCommands: this.pending.size,
        pendingQueries: this.pendingQueries.size,
      });
      // Re-run snapshot-then-delta for every live topic on (re)connect, then flush
      // any commands buffered while the socket was down.
      for (const topic of this.handlers.keys()) this.sendRaw({ kind: "subscribe", topic });
      this.flushOutbox();
    };
    sock.onmessage = (raw) => this.onMessage(raw);
    sock.onclose = (event) => {
      if (this.socket !== sock) return;
      this.socket = null;
      this.settleLostRequests();
      if (event?.code === 1001 && event.reason === "engine stopped") {
        this.terminal = true;
        this.setState("stopped");
        uiLog.info("engine stopped");
        return;
      }
      this.setState("reconnecting");
      const reconnectAttempt = this.attempt + 1;
      const delay = this.backoff(this.attempt++);
      uiLog.warn("ws disconnected", {
        reconnectAttempt,
        retryMs: delay,
        outbox: this.outbox.length,
        pendingCommands: this.pending.size,
        pendingQueries: this.pendingQueries.size,
      });
      this.opts.setTimeout(() => this.connect(), delay);
    };
  }

  private onMessage(raw: string): void {
    const msg: ServerMessage | null = decodeServerMessage(raw);
    if (!msg) {
      this.malformedFrames++;
      if (this.malformedFrames === 1 || (this.malformedFrames - 1) % 100 === 0) {
        uiLog.warn("malformed websocket frame dropped", {
          count: this.malformedFrames,
          length: raw.length,
        });
      }
      return;
    }
    switch (msg.kind) {
      case "snapshot":
      case "delta": {
        perf.countMessage(msg.topic); // no-op while perf is disabled (the default)
        const set = this.handlers.get(msg.topic);
        set?.forEach((h) => h(msg));
        return;
      }
      case "ack": {
        const pending = this.pending.get(msg.corrId);
        if (pending) {
          this.pending.delete(msg.corrId);
          if (msg.status !== "accepted") {
            uiLog.warn(`command rejected command=${pending.command} corrId=${msg.corrId} status=${msg.status} reason=${msg.reason ?? ""}`, {
              command: pending.command,
              corrId: msg.corrId,
              status: msg.status,
              reason: msg.reason,
            });
          }
          pending.resolve(msg);
        }
        return;
      }
      case "pong": {
        const receivedAt = this.opts.now();
        const rtt = Math.max(0, receivedAt - msg.t);
        this.lastRtt = rtt;
        if (msg.engineTimeMs !== undefined && msg.marketOffsetMs !== undefined
          && msg.marketSampleAgeMs !== undefined && msg.marketSampleRttMs !== undefined) {
          const midpoint = msg.t + rtt / 2;
          const browserToEngineOffsetMs = msg.engineTimeMs - midpoint;
          const effectiveOffsetMs = browserToEngineOffsetMs + msg.marketOffsetMs;
          if (Number.isFinite(effectiveOffsetMs) && Number.isFinite(browserToEngineOffsetMs)) {
            this.opts.onMarketClockSample?.({
              effectiveOffsetMs,
              browserToEngineOffsetMs,
              marketOffsetMs: msg.marketOffsetMs,
              engineTimeMs: msg.engineTimeMs,
              browserRttMs: rtt,
              marketSampleAgeMs: msg.marketSampleAgeMs,
              marketSampleRttMs: msg.marketSampleRttMs,
            });
          }
        }
        return;
      }
      case "result": {
        const pending = this.pendingQueries.get(msg.corrId);
        if (pending) { this.pendingQueries.delete(msg.corrId); pending.resolve(msg.payload); }
        return;
      }
    }
  }

  private settleLostRequests(): void {
    for (const [corrId, pending] of this.pending) {
      if (!pending.sent) continue;
      this.pending.delete(corrId);
      pending.resolve({ kind: "ack", corrId, status: "blocked", reason: "websocket disconnected", ambiguous: true });
    }
    for (const [corrId, pending] of this.pendingQueries) {
      if (!pending.sent) continue;
      this.pendingQueries.delete(corrId);
      pending.reject(new Error("websocket disconnected"));
    }
  }

  private sendRaw(msg: ClientMessage): boolean {
    if (this.state === "open" && this.socket) {
      this.socket.send(encodeClientMessage(msg));
      return true;
    }
    // Not open: buffer requests (each carries a pending promise); drop subscribe/
    // unsubscribe (reconstructed from handlers on open) and pings (re-fired on interval).
    if (msg.kind === "command" || msg.kind === "query") this.outbox.push({ raw: encodeClientMessage(msg), corrId: msg.corrId });
    return false;
  }

  private flushOutbox(): void {
    if (!this.socket) return;
    const queued = this.outbox.splice(0);
    for (const item of queued) {
      this.socket.send(item.raw);
      const command = this.pending.get(item.corrId);
      if (command) command.sent = true;
      const query = this.pendingQueries.get(item.corrId);
      if (query) query.sent = true;
    }
    if (queued.length > 0) uiLog.debug("ws outbox flushed", { count: queued.length });
  }
}
