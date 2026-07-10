import type { RafLike, Surface } from "./surface";
import { perf as defaultPerf, type PerfMonitor } from "../perf/PerfMonitor";

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

  // perf defaults to the shared singleton so every real Scheduler instance
  // reports into the same PerfMonitor without callers threading it through;
  // tests that construct `new Scheduler(fakeRaf, onErr)` get the singleton
  // too, which is a no-op while disabled (the default), so this is invisible
  // to Scheduler.test.ts.
  constructor(
    private readonly raf: RafLike,
    private readonly onPainterError: (id: string, err: unknown) => void,
    private readonly perf: PerfMonitor = defaultPerf,
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
    this.perf.frameTick(); // no-op while perf is disabled (the default)
    for (const s of [...this.surfaces.values()]) {
      if (!s.isDirty()) continue;
      // Reading `perf.enabled` (a plain boolean field) and branching on it
      // here — rather than always calling performance.now() and letting
      // recordPaint() decide — is what keeps the disabled path allocation-
      // and clock-read-free: `start` is just `0` when off.
      const timed = this.perf.enabled;
      const start = timed ? performance.now() : 0;
      try {
        s.paint();
        if (timed) this.perf.recordPaint(s.id, performance.now() - start);
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
