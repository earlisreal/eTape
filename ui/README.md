# UI

Named workspaces are cataloged in engine config (`windows.v1`) by stable UUID; `main` is unnamed and immutable. `monitoring` is the reserved permanent Monitoring Workspace: the catalog opens or reuses its named browser window, and its first missing document is seeded with four unassigned charts, a Scanner, and unassigned Stock Info. Browser Web Locks serialize catalog edits and prevent deletion while a workspace is open.

React/Vite shell around imperative market-data stores and renderers. Wire messages enter `src/wire`, route into `src/data`, then panels/controllers schedule chart or canvas work. The Account panel keeps live Open Orders and the read-only Closed Orders projection in the imperative execution store; the upper tab is session-local while each table's sort preference is persisted. Time & Sales ticks retain the engine-stamped Significant Print level in the imperative tape ring; the separate `md.tape.status` read model feeds read-only settings text. React owns layout/settings, never high-frequency payload state.

Each Scanner Panel persists its own filters with its workspace document and
keeps a separate sticky board, sorting, seen state and sound eligibility in
`ScannerStore`. The engine shares discovery and request budgets across panels.
Closing a workspace pauses its Scanner demand; an enabled Monitoring workspace
keeps following its selected Scanner Source, including when that source window
is closed. Session Volume sorts by current session shares and reports the age
of its shared top-200 discovery page.
The oldest quote timestamp appears inside the Scanner's filter settings.
Clicking, double-clicking or right-clicking a new arrival acknowledges it only
in that Scanner Panel and clears its amber fill and bold text. Single-click
selection keeps a thin outline without an amber fill; double-click loads the
symbol into the linked group.

Grouped charts also project open orders into an imperative overlay: yellow
LIMIT and cyan pretrigger STOP_LIMIT/LIT lines, price-axis chips, and per-order
drag/cancel controls. The overlay follows the ordinary execution store; market
ticks remain outside React state. Engine-held STOP_LIMIT and LIT are labeled
as local custody before trigger and become venue LIMITs only after a fresh
eligible print.

Settings → Orders & hotkeys → Add → Chart Risk Entry creates a risk preset and
hotkey. Focus a grouped chart, press the hotkey, then click the buy trigger and
sell stop or drag between them. A compact, transparent bar floats over the bottom
Volume area of the main chart pane, keeping the time axis and chart dimensions
unchanged. It shows whole shares, execution limits, notional and estimated risk
after cushions, plus custody, expiry and warnings. Drag either endpoint to adjust;
Enter submits and Escape cancels. Right-click, focus loss and context changes cancel.
The global auto-send setting defaults off and applies only to initial setup.
Submitted linked markers always request edits on release, showing before/after
risk and an above-budget warning after activation. Cancel Protection leaves
filled shares open; interruptions and expiry appear as chart warnings.
Cash % and BP % configure loss budgets, excluding fees and execution risk.

Built-in and newly added symbol-bearing panels start unassigned. General Layout downloads preserve workspace structure and non-symbol settings while removing panel and Link Group focused symbols; Monitoring Sync intent remains enabled but imports paused without a portable Scanner Source. Existing saved workspaces and downloaded files are left untouched.

The 10-second chart renders missing weekday 04:00–20:00 ET buckets as client-only synthetic flat bars at the preceding real close, with empty volume. These marked display bars are not stored or included in engine-computed indicators. The local Volume Indicator reads the same display stream, so it omits synthetic bars while retaining real Volume-Only bars; generation pauses at the session close until another real bar arrives.

Children: [application source](src/README.md), [mock engine](mock-engine/README.md), [E2E](e2e/README.md), [test support](test/README.md). Fixtures/assets/generated output excluded from guide leaves. Commands: `npm test`, `npm run typecheck`, `npm run lint`, `npm run e2e`.
