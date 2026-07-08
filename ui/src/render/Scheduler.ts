import type { RafLike, Surface } from "./surface";

// A single throw (e.g. a transient out-of-order data point reaching a chart
// library that validates strictly) shouldn't permanently kill a panel — only a
// painter that keeps failing frame after frame is actually broken.
const MAX_CONSECUTIVE_FAILURES = 10;

// One loop per window. Every frame, paint each dirty surface exactly once, then
// clear (the surface clears its own dirty flag inside paint()). A painter that
// fails MAX_CONSECUTIVE_FAILURES times in a row is removed and reported — one
// persistently broken panel never stalls the frame — but a transient throw is
// logged and retried on the next dirty frame instead of permanently freezing
// that panel.
export class Scheduler {
  private readonly surfaces = new Map<string, Surface>();
  private readonly failures = new Map<string, number>();
  private running = false;
  private frame: number | null = null;

  constructor(
    private readonly raf: RafLike,
    private readonly onPainterError: (id: string, err: unknown) => void,
  ) {}

  register(s: Surface): () => void {
    this.surfaces.set(s.id, s);
    this.failures.delete(s.id);
    return () => { this.surfaces.delete(s.id); this.failures.delete(s.id); };
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.schedule();
  }

  stop(): void {
    this.running = false;
    if (this.frame !== null) { this.raf.cancel(this.frame); this.frame = null; }
  }

  private schedule(): void {
    this.frame = this.raf.request(() => {
      this.frame = null;
      this.paintFrame();
      if (this.running) this.schedule();
    });
  }

  private paintFrame(): void {
    for (const s of [...this.surfaces.values()]) {
      if (!s.isDirty()) continue;
      try {
        s.paint();
        this.failures.delete(s.id);
      } catch (err) {
        const count = (this.failures.get(s.id) ?? 0) + 1;
        this.onPainterError(s.id, err);
        if (count >= MAX_CONSECUTIVE_FAILURES) {
          this.surfaces.delete(s.id);
          this.failures.delete(s.id);
        } else {
          this.failures.set(s.id, count);
        }
      }
    }
  }
}
