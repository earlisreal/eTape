import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, SessionSnapshot } from "../wire/contract";

// SessionSnapshot.mode generates as `string` (protoc-gen-go string enums don't
// narrow); annotate literal union explicitly here rather than widen callers.
// "pending" is UI-only and held only until first sys.session snapshot arrives.
export type SessionState = Omit<SessionSnapshot, "mode"> & { mode: "pending" | "live" | "demo" };

export class SessionStore extends ReactStore<SessionState> {
  constructor() {
    super({ mode: "pending" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.session") return;
    this.set(m.payload as SessionState);
  }
}
