# 22 — Move files, dialogs, exports, and external links native

**What to build:** Replace browser-owned file and external-content behavior with calling-window-owned Wails capabilities so traders can import, export, confirm, and open news safely without exposing arbitrary filesystem access or remote content to a privileged WebView.

**Blocked by:** 09 — Migrate non-execution mutations to typed bindings; 10 — Make Workspace catalog and Native Window registry canonical; 19 — Establish native focus capabilities without execution.

**Status:** ready-for-agent

- [ ] Workspace and settings import/export, trade export, image export, and applicable confirmations use explicit native operations owned by the calling Native Window rather than hidden file inputs, browser readers, Blob downloads, or ad hoc browser prompts.
- [ ] Imported content is size-bounded and schema-validated before any state is applied; cancellation, malformed input, oversize input, and read failures leave canonical state unchanged.
- [ ] Settings, trade, and image exports use bounded atomic writes so failure cannot leave a misleading partial destination.
- [ ] Native dialog lifetime suspends the calling window's order capability, and closing a dialog cannot revive a stale capability or execution target.
- [ ] Native operations expose only the specific import/export actions required by the product and provide no generic JavaScript filesystem binding.
- [ ] Rename, settings, practice, venue, and other complex text-entry flows remain React interfaces where native dialogs would reduce usability.
- [ ] External links accept only validated HTTP and HTTPS URLs, open in the system browser, reject every other scheme, and never navigate an eTape WebView to remote content.
- [ ] Automated service and UI tests cover dialog cancellation, validation failure, I/O failure, success, focus suspension, URL rejection, and system-browser dispatch.
- [ ] Packaged Windows smoke checks prove dialog ownership follows the invoking Workspace and that no remote page receives eTape bindings.
- [ ] User and developer documentation describes the native import/export behavior, bounds, failure handling, and allowed external-link schemes without exposing sensitive profile data.
