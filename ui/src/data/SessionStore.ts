import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, SessionSnapshot } from "../wire/contract";

// SessionSnapshot.mode generates as `string` (protoc-gen-go string enums don't
// narrow); annotate the literal union explicitly here rather than widen callers.
// "pending" is a UI-only value (never sent on the wire) held only until the
// first sys.session snapshot arrives — see the constructor below for why.
// "demo" mirrors "replay" as a practice-session mode (synthetic live feed,
// distinct DEMO banner) — see docs/superpowers/plans/2026-07-12-demo-ui-entry-plan.md.
export type SessionState = Omit<SessionSnapshot, "mode"> & { mode: "pending" | "live" | "replay" | "demo" };

export class SessionStore extends ReactStore<SessionState> {
  // Seeds to "pending", not "live": sys.session is a snapshot-only topic (set
  // once at engine boot, re-delivered on every (re)subscribe, never pushed as
  // a delta), so on a full page reload/first-load while the engine is
  // actually in replay/demo there'd otherwise be a sub-frame window where the
  // UI renders a confident live posture before the real mode is known. "live"
  // is the unsafe default to flash (practice orders looking live); "pending"
  // lets consumers show an honest indeterminate state instead. Resolves to
  // "live"/"replay" once the first snapshot lands (bounded to ~1 animation
  // frame on localhost).
  constructor() {
    super({ mode: "pending" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.session") return;
    this.set(m.payload as SessionState); // static topic: snapshot & delta are both full replaces
  }
}
