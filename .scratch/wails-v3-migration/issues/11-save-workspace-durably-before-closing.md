# 11 — Save Workspace durably before closing

**What to build:** Make every Workspace close wait for its current Dockview document to commit durably before the Native Window is disposed, while preserving an explicit escape hatch for a renderer that cannot respond.

**Blocked by:** 05 — Put engine lifecycle behind admission and drain; 10 — Make Workspace catalog and Native Window registry canonical.

**Status:** ready-for-agent

- [ ] Alt+F4 and the custom close control both hold the first close request until the frontend serializes the current layout and its durable save transaction commits.
- [ ] A successful close acknowledgement means the latest Workspace document, Panel Groups, Panel identities, settings, and layout version have committed and survive immediate process termination.
- [ ] Closing preserves the Workspace and is never interpreted as deletion; reopening it reproduces the last acknowledged document.
- [ ] Final disposal revokes the window's focus state, closes its Workspace Stream, and releases every session-owned subscription, demand, indicator, watcher, and backfill exactly once.
- [ ] The canonical Native Window registry and open set remove the window exactly once after a successful durable close, or as part of an explicitly chosen force-close, and never retain a stale handle or false restart-restoration intent.
- [ ] Concurrent save and close requests resolve deterministically without stale revisions, duplicate disposal, or writes after storage closes.
- [ ] A bounded timeout presents an explicit force-close choice when the WebView is hung; force-close does not claim an unsaved document was durable and does not trap the engine process.
- [ ] Automated tests cover immediate close after a layout mutation, forced termination after a successful acknowledgement, close/save races, duplicate close requests, and the hung-renderer path.
- [ ] Native smoke coverage verifies the close handshake from both Windows and application caption controls, and the affected Workspace/lifecycle guides describe the durable acknowledgement and force-close behavior.
