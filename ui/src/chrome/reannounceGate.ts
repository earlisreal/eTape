// ReannounceGate defers DemandRegistry.reannounce() across a session-mode
// change (e.g. live -> demo) so a WS reconnect never re-sends EnsureSymbol
// for demands that belonged to the *previous* mode's symbol universe. Two
// paths:
//  - unchanged mode (a plain WS blip / engine restart, same session mode
//    before and after): resolves fast, on the very first `sys.session`
//    snapshot the client receives after reconnect (one round-trip, no
//    external signal needed).
//  - changed mode (a demo-mode boundary): waits for AppShell to confirm the
//    new mode's panel/demand state has been applied (`onTransitionApplied`),
//    with a safety timeout so a panel-less/headless client — one that never
//    wires up the transition-applied signal — doesn't deadlock forever.
//
// `gate()` always waits for the *next* `onSessionMode` call to learn whether
// the mode changed; it never resolves off stale information from before it
// was invoked.
export class ReannounceGate {
  private lastMode: string;
  private readonly timeoutMs: number;
  private pending: Array<() => void> = [];
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(opts: { timeoutMs: number; initialMode: string }) {
    this.timeoutMs = opts.timeoutMs;
    this.lastMode = opts.initialMode;
  }

  gate(): Promise<void> {
    return new Promise((resolve) => {
      this.pending.push(resolve);
    });
  }

  onSessionMode(mode: string): void {
    const changed = mode !== this.lastMode;
    this.lastMode = mode;
    if (!changed) {
      this.resolvePending();
      return;
    }
    // Changed mode: hold the gate open until AppShell confirms the
    // transition, or the safety timeout elapses.
    this.clearTimer();
    this.timer = setTimeout(() => this.resolvePending(), this.timeoutMs);
  }

  onTransitionApplied(): void {
    this.resolvePending();
  }

  private resolvePending(): void {
    this.clearTimer();
    const toResolve = this.pending;
    this.pending = [];
    for (const resolve of toResolve) resolve();
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
