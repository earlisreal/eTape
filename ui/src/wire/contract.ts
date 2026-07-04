// INTERIM CONTRACT — hand-authored to mirror the engine's uihub/wsmsg Go structs.
// Superseded by tygo-generated `ui/src/gen/*` once the engine lands. Keep every
// field name identical to the approved specs so regeneration is a drop-in.

// md.indicator keying convention: single-series indicators (VWAP/EMA/SMA/VOLUME/
// DELTA) stream under the bare instanceId as the message `key`. Multi-series
// indicators (currently only MACD) stream each sub-series under
// `${instanceId}#${slot}` as the message key (e.g. `macd-1#macd`, `macd-1#signal`,
// `macd-1#hist`) — see `render/chart/indicatorSeries.ts`'s `describeIndicator` for
// the slot names per type. The engine must emit one snapshot/delta per slot.
export type TopicName =
  | "md.quote" | "md.book" | "md.tape" | "md.bars" | "md.indicator"
  | "scanner.rank" | "scanner.hit"
  | "news.item"
  | "exec.account" | "exec.positions" | "exec.orders" | "exec.fills" | "exec.status"
  | "sys.health" | "sys.events"
  | "config";

// ---- payloads (extend as later plans need them) ----
export interface Quote { symbol: string; bid: number; ask: number; last: number; ts: string }
export interface BookLevel { price: number; size: number }
export interface Book { symbol: string; bids: BookLevel[]; asks: BookLevel[]; ts: string }
export type TickDirection = "BUY" | "SELL" | "NEUTRAL";
export interface Tick { symbol: string; price: number; size: number; direction: TickDirection; ts: string }
export interface Bar {
  symbol: string; timeframe: string; bucketStart: string;
  o: number; h: number; l: number; c: number; v: number;
  inProgress: boolean; gap?: boolean;
}
export interface HealthLink {
  link: "ui-engine" | "engine-moomoo" | "engine-tz";
  ms: number | null; min: number | null; avg: number | null; max: number | null;
  status: "ok" | "degraded" | "down";
}
export interface HealthSnapshot { links: HealthLink[] }
export interface SysEvent { seq: number; ts: string; kind: string; detail: string }

// ---- server → client ----
export interface SnapshotMsg { kind: "snapshot"; topic: TopicName; key?: string; payload: unknown }
export interface DeltaMsg { kind: "delta"; topic: TopicName; key?: string; payload: unknown }
export interface AckMsg { kind: "ack"; corrId: string; status: "accepted" | "blocked"; reason?: string }
export interface PongMsg { kind: "pong"; t: number }
export type ServerMessage = SnapshotMsg | DeltaMsg | AckMsg | PongMsg;

// ---- client → server ----
export interface SubscribeMsg { kind: "subscribe"; topic: TopicName }
export interface UnsubscribeMsg { kind: "unsubscribe"; topic: TopicName }
export interface CommandMsg { kind: "command"; corrId: string; name: string; args: unknown }
export interface PingMsg { kind: "ping"; t: number }
export type ClientMessage = SubscribeMsg | UnsubscribeMsg | CommandMsg | PingMsg;
