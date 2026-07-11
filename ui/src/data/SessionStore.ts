import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, SessionSnapshot } from "../wire/contract";

// SessionSnapshot.mode generates as `string` (protoc-gen-go string enums don't
// narrow); annotate the literal union explicitly here rather than widen callers.
export type SessionState = Omit<SessionSnapshot, "mode"> & { mode: "live" | "replay" };

export class SessionStore extends ReactStore<SessionState> {
  constructor() {
    super({ mode: "live" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.session") return;
    this.set(m.payload as SessionState); // static topic: snapshot & delta are both full replaces
  }
}
