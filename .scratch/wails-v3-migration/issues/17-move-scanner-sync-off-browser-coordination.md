# 17 — Move Scanner Sync off browser coordination

**What to build:** Make Scanner Sync and Monitoring Workspace projections consume canonical revisioned state so multiple Workspace windows stay consistent without relying on browser broadcasts or locks.

**Blocked by:** 06 — Connect one Workspace end-to-end through Wails Stream; 10 — Make Workspace catalog and Native Window registry canonical; 13 — Add verified profile backup and additive migrations.

**Status:** ready-for-agent

- [ ] Scanner Sync state has one canonical owner, validated mutations, snapshots, and monotonic revisions shared by all Workspace windows.
- [ ] Monitoring retains its reserved identity and rename/delete protections while its Panel Groups remain editable and its scanner projections continue to update.
- [ ] Targeted changes travel through the owning Workspace Stream; ordinary Wails events, when used, are bounded app-wide identity/revision hints and are never required for correctness.
- [ ] Frontends register for changes before loading their snapshot, ignore stale revisions, and recover a missed or gapped hint by fetching canonical state.
- [ ] Four Workspaces converge after concurrent Scanner changes, reload, close/reopen, and restart without BroadcastChannel, Web Locks, durable local storage, or browser window naming.
- [ ] A slow or reloading Workspace cannot block Scanner updates to another Workspace or leave the reloaded projection permanently stale.
- [ ] Tests cover concurrent mutation, reordered or missed hints, Stream reconnect, Monitoring behavior, four-window convergence, slow consumers, and restart persistence.
- [ ] Scanner, Monitoring, shared-state, and transport guides document canonical ownership, revision recovery, and the limited role of app-wide hints.
