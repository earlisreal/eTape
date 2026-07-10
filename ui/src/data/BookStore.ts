import { PaintStore } from "./store";
import type { Book, SnapshotMsg, DeltaMsg } from "../wire/contract";

export class BookStore extends PaintStore {
  private readonly books = new Map<string, Book>();
  // Bumped per-symbol on every apply() for that symbol — backs the symbol-scoped
  // getRev(symbol) overload below. The base PaintStore's own rev counter (bumped
  // via markDirty()) still backs the no-arg getRev() global fallback, unchanged.
  private readonly revs = new Map<string, number>();

  // The engine pushes the full 10-level book every time; snapshot and delta are
  // both full replaces (replace is cheaper than diff at ~20 rows).
  apply(m: SnapshotMsg | DeltaMsg): void {
    const b = m.payload as Book;
    this.books.set(b.symbol, b);
    this.revs.set(b.symbol, (this.revs.get(b.symbol) ?? 0) + 1);
    this.markDirty();
  }

  get(symbol: string): Book | undefined { return this.books.get(symbol); }

  /**
   * Per-symbol revision when `symbol` is given (starts at 0, increments by 1
   * on each apply() for that symbol). The no-arg form is the existing global
   * PaintStore revision — back-compat for callers not yet migrated to
   * symbol-scoped reads (see Task 4, LadderPanel).
   */
  getRev(symbol?: string): number {
    if (symbol === undefined) return super.getRev();
    return this.revs.get(symbol) ?? 0;
  }
}
