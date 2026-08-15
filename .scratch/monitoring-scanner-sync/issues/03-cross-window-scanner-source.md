# 03 — Follow Scanner Sources Across Windows

**What to build:** Extend Scanner Sync so that any Scanner Panel in any workspace can be the explicit Scanner Source for Monitoring. A trader selects the source from that Scanner's header; Monitoring then follows the source's persisted visible sort and current Scanner results even after the source host window closes. Sync pauses, rather than silently retargeting, if the selected Scanner Panel is deleted.

**Blocked by:** 02 — Follow Monitoring's Scanner.

**Status:** ready-for-agent

- [ ] Every Scanner Panel exposes a clear Follow Monitoring state and can become the one active Scanner Source.
- [ ] The persisted source uses stable workspace and Scanner Panel identities, not a title or window name.
- [ ] Selecting a new Scanner Source explicitly replaces the old one and enables Sync.
- [ ] The source's stored visible sort continues to determine Monitoring rank order after its host window closes.
- [ ] Deleting the selected Scanner Panel leaves Sync enabled but paused and never chooses another Scanner automatically.
- [ ] A trader can explicitly select a replacement Scanner Source and resume Sync.
- [ ] Cross-window integration tests cover source selection, source switching, host-window closure, source deletion, persisted restart behavior, and no-automatic-retarget behavior.
