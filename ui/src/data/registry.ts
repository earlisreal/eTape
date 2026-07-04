import type { SnapshotMsg, DeltaMsg, TopicName } from "../wire/contract";
import { QuoteStore } from "./QuoteStore";
import { BookStore } from "./BookStore";
import { TapeRing } from "./TapeRing";
import { BarStore } from "./BarStore";
import { HealthStore } from "./HealthStore";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";

export interface Stores {
  quote: QuoteStore;
  book: BookStore;
  tape: TapeRing;
  bars: BarStore;
  health: HealthStore;
  exec: ExecStore;
  scanner: ScannerStore;
  news: NewsStore;
}

export function makeStores(): Stores {
  return {
    quote: new QuoteStore(),
    book: new BookStore(),
    tape: new TapeRing(),
    bars: new BarStore(),
    health: new HealthStore(),
    exec: new ExecStore(),
    scanner: new ScannerStore(),
    news: new NewsStore(),
  };
}

export function routeToStore(stores: Stores, m: SnapshotMsg | DeltaMsg): void {
  switch (m.topic) {
    case "md.quote": stores.quote.apply(m); return;
    case "md.book": stores.book.apply(m); return;
    case "md.tape": stores.tape.apply(m); return;
    case "md.bars": stores.bars.apply(m); return;
    case "md.indicator": return; // Plan 2 adds an IndicatorStore
    case "scanner.rank":
    case "scanner.hit": stores.scanner.apply(m); return;
    case "news.item": stores.news.apply(m); return;
    case "exec.account":
    case "exec.positions":
    case "exec.orders":
    case "exec.fills":
    case "exec.status": stores.exec.apply(m); return;
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
