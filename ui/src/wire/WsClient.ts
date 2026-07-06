import type {
  AckMsg, ClientMessage, DeltaMsg, ServerMessage, SnapshotMsg, TopicName,
} from "./contract";
import { decodeServerMessage, encodeClientMessage } from "./codec";

export interface ISocket {
  send(data: string): void;
  close(): void;
  onopen: (() => void) | null;
  onmessage: ((data: string) => void) | null;
  onclose: (() => void) | null;
}
export type SetTimeoutLike = (fn: () => void, ms: number) => unknown;
export type ConnState = "connecting" | "open" | "reconnecting";
type TopicHandler = (m: SnapshotMsg | DeltaMsg) => void;

interface Opts {
  url: string;
  socketFactory: (url: string) => ISocket;
  now: () => number;
  setTimeout: SetTimeoutLike;
  backoff?: (attempt: number) => number;
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
  private readonly pending = new Map<string, (ack: AckMsg) => void>();
  private readonly pendingQueries = new Map<string, (payload: unknown) => void>();
  private readonly outbox: string[] = []; // commands buffered while not open
  private readonly backoff: (attempt: number) => number;

  constructor(private readonly opts: Opts) {
    this.backoff = opts.backoff ?? DEFAULT_BACKOFF;
  }

  start(): void { this.connect(); }
  stop(): void { this.socket?.close(); this.socket = null; }

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
      this.pending.set(corrId, resolve);
      this.sendRaw({ kind: "command", corrId, name, args });
    });
  }

  sendQuery(name: string, args: unknown): Promise<unknown> {
    const corrId = `q${++this.corr}`;
    return new Promise<unknown>((resolve) => {
      this.pendingQueries.set(corrId, resolve);
      this.sendRaw({ kind: "query", corrId, name, args });
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
    this.setState("connecting");
    const sock = this.opts.socketFactory(this.opts.url);
    this.socket = sock;
    sock.onopen = () => {
      this.attempt = 0;
      this.setState("open");
      // Re-run snapshot-then-delta for every live topic on (re)connect, then flush
      // any commands buffered while the socket was down.
      for (const topic of this.handlers.keys()) this.sendRaw({ kind: "subscribe", topic });
      this.flushOutbox();
    };
    sock.onmessage = (raw) => this.onMessage(raw);
    sock.onclose = () => {
      if (this.socket !== sock) return;
      this.socket = null;
      this.setState("reconnecting");
      const delay = this.backoff(this.attempt++);
      this.opts.setTimeout(() => this.connect(), delay);
    };
  }

  private onMessage(raw: string): void {
    const msg: ServerMessage | null = decodeServerMessage(raw);
    if (!msg) return; // drop-and-count malformed frames
    switch (msg.kind) {
      case "snapshot":
      case "delta": {
        const set = this.handlers.get(msg.topic);
        set?.forEach((h) => h(msg));
        return;
      }
      case "ack": {
        const resolve = this.pending.get(msg.corrId);
        if (resolve) { this.pending.delete(msg.corrId); resolve(msg); }
        return;
      }
      case "pong": {
        this.lastRtt = this.opts.now() - msg.t;
        return;
      }
      case "result": {
        const resolve = this.pendingQueries.get(msg.corrId);
        if (resolve) { this.pendingQueries.delete(msg.corrId); resolve(msg.payload); }
        return;
      }
    }
  }

  private sendRaw(msg: ClientMessage): void {
    if (this.state === "open" && this.socket) {
      this.socket.send(encodeClientMessage(msg));
      return;
    }
    // Not open: buffer commands (each carries a pending promise); drop subscribe/
    // unsubscribe (reconstructed from handlers on open) and pings (re-fired on interval).
    if (msg.kind === "command" || msg.kind === "query") this.outbox.push(encodeClientMessage(msg));
  }

  private flushOutbox(): void {
    if (!this.socket) return;
    for (const raw of this.outbox.splice(0)) this.socket.send(raw);
  }
}
