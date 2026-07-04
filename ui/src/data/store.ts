// Two store flavors, one per data plane:
//  - PaintStore feeds canvas painters at message rate; the scheduler polls isDirty().
//  - ReactStore feeds low-rate chrome via useSyncExternalStore.
// Neither imports React; ReactStore is React-shaped but React-free.

export abstract class PaintStore {
  private dirty = false;
  private rev = 0;
  protected markDirty(): void { this.dirty = true; this.rev++; }
  isDirty(): boolean { return this.dirty; }
  consumeDirty(): boolean { const d = this.dirty; this.dirty = false; return d; }
  // Multi-consumer alternative to isDirty()/consumeDirty(): unlike consumeDirty(),
  // reading the revision never resets state for other consumers, so N independent
  // surfaces (e.g. several chart panels sharing one BarStore) can each track their
  // own "have I seen this change yet" cursor without starving each other.
  getRev(): number { return this.rev; }
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
