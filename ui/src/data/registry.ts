import type { SnapshotMsg, DeltaMsg, TopicName } from "../wire/contract";
import { QuoteStore } from "./QuoteStore";
import { BookStore } from "./BookStore";
import { TapeRing } from "./TapeRing";
import { BarStore } from "./BarStore";
import { IndicatorStore } from "./IndicatorStore";
import { HealthStore } from "./HealthStore";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";
import { FillStore } from "./FillStore";
import { TradeStore } from "./TradeStore";
import { DrawingStore } from "../render/chart/drawings/store";
import { DrawingToolStyleStore } from "../render/chart/drawings/toolStyles";

export interface Stores {
  quote: QuoteStore;
  book: BookStore;
  tape: TapeRing;
  bars: BarStore;
  indicators: IndicatorStore;
  health: HealthStore;
  exec: ExecStore;
  scanner: ScannerStore;
  news: NewsStore;
  fills: FillStore;
  trades: TradeStore;
  drawings: DrawingStore;
  drawingToolStyles: DrawingToolStyleStore;
}

export function makeStores(): Stores {
  return {
    quote: new QuoteStore(),
    book: new BookStore(),
    tape: new TapeRing(),
    bars: new BarStore(),
    indicators: new IndicatorStore(),
    health: new HealthStore(),
    exec: new ExecStore(),
    scanner: new ScannerStore(),
    news: new NewsStore(),
    fills: new FillStore(),
    trades: new TradeStore(),
    drawings: new DrawingStore(),
    drawingToolStyles: new DrawingToolStyleStore(),
  };
}

export function routeToStore(stores: Stores, m: SnapshotMsg | DeltaMsg): void {
  switch (m.topic) {
    case "md.quote": stores.quote.apply(m); return;
    case "md.book": stores.book.apply(m); return;
    case "md.tape": stores.tape.apply(m); return;
    case "md.bars": stores.bars.apply(m); return;
    case "md.indicator": stores.indicators.apply(m); return;
    case "scanner.rank":
    case "scanner.hit": stores.scanner.apply(m); return;
    case "news.item": stores.news.apply(m); return;
    case "exec.account":
    case "exec.positions":
    case "exec.orders":
    case "exec.status": stores.exec.apply(m); return;
    case "exec.fills": stores.fills.apply(m); return;
    case "exec.trades": stores.trades.apply(m); return;
    case "sys.health":
    case "sys.events": stores.health.apply(m); return;
    case "config": return; // handled by workspace.ts, not a store
  }
}

export interface TopicSubscriber {
  subscribe(topic: TopicName, cb: (m: SnapshotMsg | DeltaMsg) => void): () => void;
}

export function connectStores(client: TopicSubscriber, stores: Stores, topics: TopicName[]): () => void {
  const offs = topics.map((t) => client.subscribe(t, (m) => routeToStore(stores, m)));
  return () => offs.forEach((off) => off());
}
