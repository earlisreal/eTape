// Two store flavors, one per data plane:
//  - PaintStore feeds canvas painters at message rate; the scheduler polls isDirty().
//  - ReactStore feeds low-rate chrome via useSyncExternalStore.
// Neither imports React; ReactStore is React-shaped but React-free.

export abstract class PaintStore {
  private dirty = false;
  protected markDirty(): void { this.dirty = true; }
  isDirty(): boolean { return this.dirty; }
  consumeDirty(): boolean { const d = this.dirty; this.dirty = false; return d; }
}

export abstract class ReactStore<S> {
  private snapshot: S;
  private readonly subs = new Set<() => void>();
  constructor(initial: S) { this.snapshot = initial; }

  subscribe(cb: () => void): () => void { this.subs.add(cb); return () => this.subs.delete(cb); }
  getSnapshot(): S { return this.snapshot; }
  protected set(next: S): void { this.snapshot = next; this.emit(); }
  protected emit(): void { this.subs.forEach((cb) => cb()); }
}
