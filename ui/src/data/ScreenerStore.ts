import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, ScreenerState } from "../wire/contract";

export class ScreenerStore extends ReactStore<ReadonlyMap<string, ScreenerState>> {
  constructor() { super(new Map()); }
  apply(message: SnapshotMsg | DeltaMsg): void {
    const state = message.payload as ScreenerState;
    const previous = this.getSnapshot().get(state.panelId);
    if (message.kind === "delta" && previous && state.runId < previous.runId) return;
    const next = new Map(this.getSnapshot());
    if (state.status === "removed") next.delete(state.panelId);
    else next.set(state.panelId, { ...state, rows: state.rows ?? [] });
    this.set(next);
  }
}
