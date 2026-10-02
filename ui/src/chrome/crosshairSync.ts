import type { LinkGroup } from "./linkGroups";
import { TIMEFRAMES, type Timeframe } from "../render/chart/barBucket";

const CHANNEL_NAME = "etape.crosshair";
const HEARTBEAT_MS = 1_000;
const EXPIRE_MS = 3_500;
export interface CrosshairCursor {
  group: Exclude<LinkGroup, null>;
  symbol: string;
  timeframe: Timeframe;
  timeMs: number;
  price: number | null;
}

export interface CrosshairContext { group: LinkGroup; symbol: string; enabled: boolean }
type ActiveCrosshairContext = Omit<CrosshairContext, "group"> & { group: Exclude<LinkGroup, null> };

export interface CrosshairSyncHandle {
  publish: (cursor: CrosshairCursor) => void;
  clear: () => void;
  refresh: () => void;
  dispose: () => void;
}

interface CrosshairMessage extends CrosshairCursor {
  kind: "move" | "clear" | "heartbeat";
  sourceId: string;
  sentAt: number;
  sequence: number;
}

interface OwnedCursor {
  message: CrosshairMessage;
  receivedAt: number;
  local: boolean;
}

type Listener = (cursor: CrosshairCursor | null) => void;
type ChannelFactory = () => BroadcastChannel;

export class CrosshairSync {
  private channel: BroadcastChannel | null = null;
  private readonly participants = new Map<string, { context: () => CrosshairContext; listener: Listener }>();
  private readonly local = new Map<string, CrosshairMessage>();
  private readonly sequences = new Map<string, number>();
  private readonly seenSequences = new Map<string, number>();
  private readonly latest = new Map<string, OwnedCursor>();
  private timer: ReturnType<typeof setInterval> | null = null;
  private readonly onMessage = (event: MessageEvent<unknown>) => this.receive(event.data);
  private readonly onVisibility = () => {
    if (document.visibilityState === "hidden") {
      for (const sourceId of this.local.keys()) this.clear(sourceId);
    } else {
      this.prune();
    }
  };

  constructor(private readonly createChannel: ChannelFactory = () => new BroadcastChannel(CHANNEL_NAME)) {}

  register(context: () => CrosshairContext, listener: Listener): CrosshairSyncHandle {
    this.open();
    const sourceId = crypto.randomUUID();
    this.participants.set(sourceId, { context, listener });
    const active = this.activeCursor(context());
    if (active) listener(this.latest.get(this.key(active))?.message ?? null);

    return {
      publish: (cursor) => this.publish(sourceId, context, cursor),
      clear: () => this.clear(sourceId),
      refresh: () => {
        const current = this.participants.get(sourceId);
        if (!current) return;
        const position = this.activeCursor(current.context());
        const message = position ? this.latest.get(this.key(position))?.message : undefined;
        current.listener(message?.sourceId === sourceId ? null : message ?? null);
      },
      dispose: () => {
        this.clear(sourceId);
        this.participants.delete(sourceId);
        this.sequences.delete(sourceId);
        if (this.participants.size === 0) this.close();
      },
    };
  }

  private open(): void {
    if (this.channel) return;
    this.channel = this.createChannel();
    this.channel.addEventListener("message", this.onMessage);
    if (typeof document !== "undefined") document.addEventListener("visibilitychange", this.onVisibility);
  }

  private close(): void {
    if (this.timer !== null) clearInterval(this.timer);
    this.timer = null;
    this.latest.clear();
    this.sequences.clear();
    this.seenSequences.clear();
    if (typeof document !== "undefined") document.removeEventListener("visibilitychange", this.onVisibility);
    this.channel?.removeEventListener("message", this.onMessage);
    this.channel?.close();
    this.channel = null;
  }

  private publish(sourceId: string, contextReader: () => CrosshairContext, cursor: CrosshairCursor): void {
    const context = this.activeCursor(contextReader());
    if (!context || context.group !== cursor.group || context.symbol !== cursor.symbol
      || !TIMEFRAMES.includes(cursor.timeframe) || !Number.isFinite(cursor.timeMs)
      || (cursor.price !== null && !Number.isFinite(cursor.price))) {
      this.clear(sourceId);
      return;
    }
    const previous = this.local.get(sourceId);
    if (previous && this.key(previous) !== this.key(cursor)) this.clear(sourceId);
    const current = this.local.get(sourceId);
    const sequence = (this.sequences.get(sourceId) ?? current?.sequence ?? 0) + 1;
    this.sequences.set(sourceId, sequence);
    const message: CrosshairMessage = { ...cursor, kind: "move", sourceId, sequence, sentAt: Date.now() };
    this.local.set(sourceId, message);
    this.accept(message, true);
    this.channel?.postMessage(message);
    this.startTimer();
  }

  private clear(sourceId: string): void {
    const previous = this.local.get(sourceId);
    if (!previous) return;
    this.local.delete(sourceId);
    const sequence = (this.sequences.get(sourceId) ?? previous.sequence) + 1;
    this.sequences.set(sourceId, sequence);
    const message: CrosshairMessage = { ...previous, kind: "clear", sequence, sentAt: Date.now() };
    this.accept(message, true);
    this.channel?.postMessage(message);
    this.startTimer();
  }

  private receive(value: unknown): void {
    if (!this.isMessage(value)) return;
    this.accept(value, false);
    this.startTimer();
  }

  private accept(message: CrosshairMessage, local: boolean): void {
    const key = this.key(message);
    const current = this.latest.get(key);
    if (message.kind === "heartbeat") {
      const seen = this.seenSequences.get(message.sourceId) ?? 0;
      if (current?.message.sourceId === message.sourceId && current.message.sequence === message.sequence) {
        current.receivedAt = Date.now();
      } else if (message.sequence >= seen && (!current || this.isNewer(message, current.message))) {
        const replay = { ...message, kind: "move" as const };
        this.seenSequences.set(message.sourceId, message.sequence);
        this.latest.set(key, { message: replay, receivedAt: Date.now(), local });
        this.notify(key, message.sourceId, replay);
      }
      return;
    }
    if (message.sequence <= (this.seenSequences.get(message.sourceId) ?? 0)) return;
    this.seenSequences.set(message.sourceId, message.sequence);
    if (message.kind === "clear") {
      if (!current || current.message.sourceId !== message.sourceId || message.sequence <= current.message.sequence) return;
      this.latest.delete(key);
      this.notify(key, message.sourceId, null);
      return;
    }
    if (current && !this.isNewer(message, current.message)) return;
    this.latest.set(key, { message, receivedAt: Date.now(), local });
    this.notify(key, message.sourceId, message);
  }

  private isNewer(next: CrosshairMessage, current: CrosshairMessage): boolean {
    if (next.sourceId === current.sourceId) return next.sequence > current.sequence;
    return next.sentAt > current.sentAt || (next.sentAt === current.sentAt && next.sourceId > current.sourceId);
  }

  private notify(key: string, sourceId: string, cursor: CrosshairCursor | null): void {
    for (const [id, participant] of this.participants) {
      if (id === sourceId) continue;
      const context = this.activeCursor(participant.context());
      if (context && this.key(context) === key) participant.listener(cursor);
    }
  }

  private activeCursor(context: CrosshairContext): ActiveCrosshairContext | null {
    return context.enabled && context.group !== null && context.symbol ? { ...context, group: context.group } : null;
  }

  private key(cursor: Pick<CrosshairCursor, "group" | "symbol">): string {
    return `${cursor.group}\u0000${cursor.symbol}`;
  }

  private isMessage(value: unknown): value is CrosshairMessage {
    if (typeof value !== "object" || value === null) return false;
    const message = value as Partial<CrosshairMessage>;
    return (message.kind === "move" || message.kind === "clear" || message.kind === "heartbeat")
      && typeof message.sourceId === "string" && message.sourceId.length > 0
      && (message.group === "red" || message.group === "green" || message.group === "blue" || message.group === "yellow")
      && typeof message.symbol === "string" && message.symbol.length > 0
      && TIMEFRAMES.includes(message.timeframe as Timeframe)
      && Number.isFinite(message.timeMs) && (message.price === null || Number.isFinite(message.price))
      && typeof message.sentAt === "number" && Number.isFinite(message.sentAt) && message.sentAt >= 0
      && typeof message.sequence === "number" && Number.isSafeInteger(message.sequence) && message.sequence > 0;
  }

  private startTimer(): void {
    if (this.timer !== null) return;
    this.timer = setInterval(() => {
      for (const message of this.local.values()) {
        this.channel?.postMessage({ ...message, kind: "heartbeat" });
      }
      this.prune();
    }, HEARTBEAT_MS);
  }

  private prune(): void {
    const now = Date.now();
    for (const [key, cursor] of this.latest) {
      if (!cursor.local && now - cursor.receivedAt >= EXPIRE_MS) {
        this.latest.delete(key);
        this.notify(key, cursor.message.sourceId, null);
      }
    }
    this.stopTimerWhenIdle();
  }

  private stopTimerWhenIdle(): void {
    if (this.local.size || [...this.latest.values()].some((cursor) => !cursor.local)) return;
    if (this.timer !== null) clearInterval(this.timer);
    this.timer = null;
  }
}
