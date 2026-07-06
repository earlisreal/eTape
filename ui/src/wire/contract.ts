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

// ---- scanner (Plan 4) ----
// Session travels on the message `key` ("premarket" | "rth" | "afterhours").
export type ScannerSession = "premarket" | "rth" | "afterhours";
export interface ScannerRow {
  symbol: string;
  changePct: number | null;   // % change; null = no print yet (never a fabricated 0)
  last: number | null;        // last trade price; null = no print yet
  floatShares: number | null; // true free float in ACTUAL shares (engine already
                              // converts moomoo's thousands unit); null = unknown
  volume: number;             // session cumulative volume (0 is legitimate)
}
export interface ScannerRankPayload { refreshedAt: string; rows: ScannerRow[] } // one full ranking
export interface ScanHitPayload { symbol: string; at: string }                  // explicit new-qualifier event

// ---- news (Plan 4) ----
export interface NewsItem { symbol: string; headline: string; source: string; url: string; seen_at: string }

// ---- execution (Plan 5) ----
// Field names mirror engine/internal/exec structs (engine-execution-core plan) so
// tygo-generated ui/src/gen/* is a drop-in. Venue is a free-form config slug.
export type VenueID = string;
export type Broker = "tradezero" | "alpaca" | "moomoo";
export type Side = "BUY" | "SELL" | "SHORT" | "COVER";
export type OrderType = "MARKET" | "LIMIT" | "STOP" | "STOP_LIMIT";
export type TIF = "DAY" | "GTC" | "IOC" | "FOK";
export type OrderStatus =
  | "SUBMITTED" | "ACCEPTED" | "PARTIALLY_FILLED" | "FILLED"
  | "CANCELED" | "REJECTED" | "EXPIRED" | "BLOCKED" | "REPLACED";

// exec.orders — keyed by `id`; snapshot payload = Order[], delta payload = Order (upsert).
export interface Order {
  venue: VenueID; id: string; symbol: string;
  side: Side; type: OrderType; tif: TIF;
  qty: number; limitPrice: number; stopPrice: number;
  status: OrderStatus; executedQty: number; leavesQty: number; avgFillPrice: number;
  rejectReason: string; replacesId: string;
  createdMs: number; updatedMs: number;
}
// exec.fills — append-only, keyed by symbol; snapshot payload = Fill[], delta payload = Fill.
export interface Fill { venue: VenueID; orderId: string; symbol: string; side: Side; qty: number; price: number; tsMs: number }
// exec.positions — full-replace; payload = PositionRow[]. venue===null => cross-venue net row.
export interface PositionRow { venue: VenueID | null; symbol: string; qty: number; avgPrice: number; unrealizedPnl: number }
// exec.account — keyed by venue; payload = AccountRow (upsert).
export interface AccountRow {
  venue: VenueID;
  equity: number; buyingPower: number; availableCash: number;
  sodEquity: number; realized: number; dayPnl: number; leverage: number;
  tsMs: number;
}
// exec.status — full-replace; payload = ExecStatus.
export interface GateLimitsView { maxOrderValue: number; maxPositionValue: number; maxPositionShares: number; maxOpenOrders: number }
export interface VenueStatus {
  venue: VenueID; broker: Broker; connected: boolean; venueArmed: boolean;
  reconcilePending: boolean; note: string; lastReconcileMs: number | null; gate: GateLimitsView;
}
export interface ExecStatus {
  masterArmed: boolean;
  global: { maxDayLoss: number; maxSymbolPositionValue: number; maxSymbolPositionShares: number };
  venues: VenueStatus[];
}

// ---- command args (UI → engine via CommandMsg.args) ----
export interface SubmitOrderArgs { venue: VenueID; symbol: string; side: Side; type: OrderType; tif: TIF; qty: number; limitPrice: number; stopPrice: number }
export interface CancelOrderArgs { venue: VenueID; orderId: string }
export interface ReplaceOrderArgs { venue: VenueID; orderId: string; qty: number; limitPrice: number; stopPrice: number }
export interface FlattenArgs { venue: VenueID }
export interface KillSwitchArgs { venue?: VenueID }   // omitted/empty => all venues
export interface ArmArgs { venue?: VenueID }          // omitted/empty => master

// ---- server → client ----
export interface SnapshotMsg { kind: "snapshot"; topic: TopicName; key?: string; payload: unknown }
export interface DeltaMsg { kind: "delta"; topic: TopicName; key?: string; payload: unknown }
export interface AckMsg {
  kind: "ack"; corrId: string;
  status: "accepted" | "blocked"; reason?: string;
  orderId?: string;    // returned on a SubmitOrder accept — keys the optimistic PendingNew row
  value?: unknown;     // returned on a GetConfig accept (already relied on by ThemeProvider/WorkspaceStore)
}
export interface PongMsg { kind: "pong"; t: number }
export interface ResultMsg { kind: "result"; corrId: string; payload: unknown }
export type ServerMessage = SnapshotMsg | DeltaMsg | AckMsg | PongMsg | ResultMsg;

// ---- client → server ----
export interface SubscribeMsg { kind: "subscribe"; topic: TopicName }
export interface UnsubscribeMsg { kind: "unsubscribe"; topic: TopicName }
export interface CommandMsg { kind: "command"; corrId: string; name: string; args: unknown }
export interface QueryMsg { kind: "query"; corrId: string; name: string; args: unknown }
export interface PingMsg { kind: "ping"; t: number }
export type ClientMessage = SubscribeMsg | UnsubscribeMsg | CommandMsg | QueryMsg | PingMsg;
