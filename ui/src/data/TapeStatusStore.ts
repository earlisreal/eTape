import { ReactStore } from "./store";
import type { DeltaMsg, SignificanceStatus, SnapshotMsg } from "../wire/contract";

export type TapeStatusState = Readonly<Record<string, SignificanceStatus>>;

/** Low-frequency classifier read model; tick annotations stay in TapeRing. */
export class TapeStatusStore extends ReactStore<TapeStatusState> {
  constructor() { super({}); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "md.tape.status") return;
    const payload = m.payload as SignificanceStatus | SignificanceStatus[] | null | undefined;
    const statuses = payload == null ? [] : Array.isArray(payload) ? payload : [payload];
    if (statuses.length === 0) return;
    const next = { ...this.getSnapshot() };
    for (const status of statuses) next[status.symbol] = status;
    this.set(next);
  }

  get(symbol: string): SignificanceStatus | undefined { return this.getSnapshot()[symbol]; }
}
