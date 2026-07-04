import type { RafLike, Surface } from "./surface";

// One loop per window. Every frame, paint each dirty surface exactly once, then
// clear (the surface clears its own dirty flag inside paint()). A painter that
// throws is removed and reported — one broken panel never stalls the frame.
export class Scheduler {
  private readonly surfaces = new Map<string, Surface>();
  private running = false;
  private frame: number | null = null;

  constructor(
    private readonly raf: RafLike,
    private readonly onPainterError: (id: string, err: unknown) => void,
  ) {}

  register(s: Surface): () => void {
    this.surfaces.set(s.id, s);
    return () => { this.surfaces.delete(s.id); };
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
      } catch (err) {
        this.surfaces.delete(s.id);
        this.onPainterError(s.id, err);
      }
    }
  }
}
