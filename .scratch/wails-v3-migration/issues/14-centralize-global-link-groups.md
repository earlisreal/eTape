# 14 — Centralize global Link Groups

**What to build:** Give every Workspace one canonical, durable red, green, blue, and yellow Link Group focus so linked Panels converge on the same validated symbol and venue without browser-to-browser coordination.

**Blocked by:** 06 — Connect one Workspace end-to-end through Wails Stream; 10 — Make Workspace catalog and Native Window registry canonical; 13 — Add verified profile backup and additive migrations.

**Status:** ready-for-agent

- [ ] Go owns one serialized, validated symbol-and-venue focus value and monotonic revision for each global Link Group.
- [ ] Panel membership in a Link Group remains part of its opaque Workspace document; this ticket migrates only the shared focus value.
- [ ] Legacy migration deterministically uses valid Main values when Workspaces disagree, and uses an empty group when Main is missing or malformed rather than selecting another Workspace.
- [ ] Conflict diagnostics contain Workspace and group identity only, never symbols, credentials, accounts, balances, or other sensitive payloads.
- [ ] Typed snapshot and mutation operations return resulting revisions, persist accepted values, and reject invalid or stale mutations without losing a newer value.
- [ ] Targeted Workspace Stream updates make four open Workspaces converge whether an update arrives before or after its binding result; stale projections are ignored and revision gaps recover from a snapshot.
- [ ] Global Link Group focus survives close and restart, while opening or reloading a Workspace receives the current canonical snapshot before subsequent changes.
- [ ] Link Group behavior no longer depends on BroadcastChannel, Web Locks, durable local storage, browser window names, or ordinary Wails event delivery.
- [ ] Tests cover concurrent focus changes, four-Workspace convergence, missed/reordered hints, Main-wins conflicts, malformed Main state, restart persistence, and migration idempotence.
- [ ] The shared-state and migration guides document global focus ownership, revision behavior, Main-wins conversion, and the separate scope of Panel membership.
