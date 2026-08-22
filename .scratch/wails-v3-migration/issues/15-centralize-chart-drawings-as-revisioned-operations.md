# 15 — Centralize Chart Drawings as revisioned operations

**What to build:** Persist Chart Drawings through canonical per-symbol operations so concurrent edits in different Workspace windows converge without one whole-symbol snapshot overwriting another.

**Blocked by:** 06 — Connect one Workspace end-to-end through Wails Stream; 10 — Make Workspace catalog and Native Window registry canonical; 13 — Add verified profile backup and additive migrations.

**Status:** ready-for-agent

- [ ] Go accepts explicit drawing upsert, remove, and clear operations and returns the resulting per-symbol revision for each accepted mutation.
- [ ] The frontend keeps its imperative optimistic Drawing projection, then reconciles it with accepted canonical operations and ignores stale revisions.
- [ ] Targeted Workspace Stream updates make every open view of a symbol converge whether the update arrives before or after the originating mutation result.
- [ ] Concurrent edits to different drawings cannot overwrite one another, and clear has deterministic revision semantics against concurrent upsert or remove operations.
- [ ] Accepted drawing state persists across Workspace close and application restart and is included in the verified additive migration without exposing drawing payloads in diagnostics.
- [ ] Whole-symbol browser broadcasts and browser-owned drawing persistence are no longer required for drawing correctness.
- [ ] A drawing changed immediately before Alt+F4, custom close, Quit, or restart is reproduced after reopening whenever its durable acknowledgement succeeded.
- [ ] Tests cover optimistic acceptance and rejection, duplicate/out-of-order delivery, concurrent upsert/remove/clear, reload snapshot ordering, restart persistence, and unrelated-operation preservation.
- [ ] The chart/shared-state guides describe canonical operation ownership, revision reconciliation, durability, and the retained imperative rendering boundary.
