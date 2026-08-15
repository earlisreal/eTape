# 02 — Follow Monitoring's Scanner

**What to build:** Deliver end-to-end Scanner Sync using the Scanner Panel inside the Monitoring Workspace as its source. The Scanner header explicitly enables or disables the persistent Follow Monitoring relationship. Enabled Sync continuously assigns the source's sorted Scanner symbols to the pinned Chart Panels through a deterministic stable-membership rule, while preserving each chart's settings and position. This ticket establishes the pure planner seam and the complete local-source behavior; cross-window source selection follows in Ticket 03.

**Blocked by:** 01 — Reserve the Monitoring Workspace.

**Status:** ready-for-agent

- [ ] The Monitoring Scanner header can select itself as Scanner Source, enable Sync, disable Sync while remembering the source, and display enabled, paused, and incomplete-following status.
- [ ] Enabled Sync and the local Scanner Source persist across restart.
- [ ] Only pinned Chart Panels participate; the target count changes with that panel count, and linked Chart Panels opt out.
- [ ] Initial ranked symbols fill chart slots in stable screen order; rank swaps do not reshuffle retained chart positions.
- [ ] A symbol that leaves the relevant ranked set is replaced by the earliest eligible incoming slot, while chart timeframe, indicators, drawings, appearance, layout, and size remain unchanged.
- [ ] Scanner changes are coalesced to at most one symbol application per second.
- [ ] Fewer Scanner rows retain unmatched chart symbols and report following coverage; no pinned charts leave Sync enabled but paused.
- [ ] Manual edits to participating pinned charts are restored on the next successful plan, while linking a chart or turning Sync off remains the opt-out path.
- [ ] Planner and workspace-shell tests cover initial fill, stable membership, rank swaps, additions/removals, link opt-out, temporary row scarcity, pause states, persistence, and preserved chart settings.
