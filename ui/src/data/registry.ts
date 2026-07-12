import type { SnapshotMsg, DeltaMsg, TopicName, Tick } from "../wire/contract";
import { perf } from "../perf/PerfMonitor";
import { QuoteStore } from "./QuoteStore";
import { BookStore } from "./BookStore";
import { TapeRing } from "./TapeRing";
import { BarStore } from "./BarStore";
import { IndicatorStore } from "./IndicatorStore";
import { HealthStore } from "./HealthStore";
import { SessionStore } from "./SessionStore";
import { BootStore } from "./BootStore";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";
import { StockDetailStore } from "./StockDetailStore";
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
  session: SessionStore;
  boot: BootStore;
  exec: ExecStore;
  scanner: ScannerStore;
  news: NewsStore;
  stockDetail: StockDetailStore;
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
    session: new SessionStore(),
    boot: new BootStore(),
    exec: new ExecStore(),
    scanner: new ScannerStore(),
    news: new NewsStore(),
    stockDetail: new StockDetailStore(),
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
    case "md.tape":
      perf.countTicks((m.payload as Tick[]).length); // no-op while perf is disabled (the default)
      stores.tape.apply(m);
      return;
    case "md.bars": stores.bars.apply(m); return;
    case "md.indicator": stores.indicators.apply(m); return;
    case "scanner.rank":
    case "scanner.hit": stores.scanner.apply(m); return;
    case "news.item": stores.news.apply(m); return;
    case "stock.detail": stores.stockDetail.apply(m); return;
    case "exec.account":
    case "exec.positions":
    case "exec.orders":
    case "exec.status": stores.exec.apply(m); return;
    case "exec.fills": stores.fills.apply(m); return;
    case "exec.trades": stores.trades.apply(m); return;
    case "sys.health":
    case "sys.events": stores.health.apply(m); return;
    case "sys.session": stores.session.apply(m); return;
    case "sys.boot": stores.boot.apply(m); return;
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
