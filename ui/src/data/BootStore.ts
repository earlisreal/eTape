import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, BootStatus } from "../wire/contract";

// BootStatus.phase generates as `string`; annotate the literal union here.
export type BootState = Omit<BootStatus, "phase"> & { phase: "connecting" | "sealing" | "ready" };

export class BootStore extends ReactStore<BootState> {
  // Seeds "connecting" (never "ready"): sys.boot is snapshot-bearing, so on a
  // fresh page load the real phase arrives within ~1 frame. "connecting" keeps
  // the red FeedStatusBanner suppressed until the engine confirms readiness,
  // and only briefly shows a neutral "Connecting…" strip — never a false red.
  constructor() {
    super({ phase: "connecting" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.boot") return;
    this.set(m.payload as BootState); // snapshot & delta are both full replaces
  }
}
