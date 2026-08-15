# 01 — Reserve the Monitoring Workspace

**What to build:** Deliver a permanent Monitoring Workspace that a trader opens or reuses as its own window instead of applying a replaceable preset over the current workspace. Its identity cannot be renamed or deleted, while its Panel Groups and layout remain editable and persist normally. On first usable open, seed four pinned, unassigned Chart Panels, a Scanner Panel, and an unassigned Stock Info Panel without overwriting existing saved Monitoring data. Give unassigned Monitoring charts the Waiting for Scanner Sync state and preserve ordinary type-to-load behavior.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] The catalog and window management experience opens or reuses one reserved Monitoring Workspace without replacing another workspace.
- [ ] Monitoring cannot be renamed or deleted, but panel and layout edits persist and reopening does not reapply the seed.
- [ ] First-use Monitoring contains four pinned, unassigned Chart Panels, a Scanner Panel, and Stock Info with no selected Link Group.
- [ ] An unassigned Monitoring Chart Panel visibly waits for Scanner Sync, has no fallback ticker, and makes no unnecessary symbol demand.
- [ ] Existing workspace data under the reserved identity is preserved rather than overwritten.
- [ ] Workspace behavior and unassigned-chart tests cover the protected identity, seed, edit persistence, and no-default-symbol behavior.
