import { PaintStore } from "./store";
import type { Book, SnapshotMsg, DeltaMsg } from "../wire/contract";

export class BookStore extends PaintStore {
  private readonly books = new Map<string, Book>();

  // The engine pushes the full 10-level book every time; snapshot and delta are
  // both full replaces (replace is cheaper than diff at ~20 rows).
  apply(m: SnapshotMsg | DeltaMsg): void {
    const b = m.payload as Book;
    this.books.set(b.symbol, b);
    this.markDirty();
  }

  get(symbol: string): Book | undefined { return this.books.get(symbol); }
}
