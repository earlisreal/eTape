import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, BootStatus } from "../wire/contract";

export type BootState = Omit<BootStatus, "phase"> & { phase: "connecting" | "ready" };

export class BootStore extends ReactStore<BootState> {
  constructor() {
    super({ phase: "connecting" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.boot") return;
    this.set(m.payload as BootState);
  }
}
