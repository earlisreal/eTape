# Chrome

Workspace shell, dock layout, settings, controls, and execution surfaces. Inputs: imperative stores plus user actions; outputs: wire commands and panel/controller lifecycle. Children: [execution UI](exec/README.md), [panels](panels/README.md), `controls`. React state stays low-frequency. Test: `npm test`.

The global Top Bar places the browser-derived ET clock and weekday session-transition countdown immediately after connection latency, centers the active hotkey target (Link Group dot, symbol, venue, and the non-interactive T-M-S Venue Instrument Eligibility group), and keeps shell actions on the right in every workspace window. Eligibility is queried for the exact ready target, refreshed every 60 seconds, and hidden when unsupported, unknown, or failed. Workspace launches request Chromium popup windows sized to the current monitor's available bounds so additional workspaces open app-like and maximized instead of as tabs.

The main workspace registers the stable browsing-context name `etape-workspace-main`. A Scanner double-click in any secondary workspace updates its Link Group, then resolves and focuses that named main window; a missing main context is opened once and reused. The browser and OS control whether focus restores a minimized or obscured window. The behavior is best effort on platforms/browsers that decline focus, and the owned Windows Chrome app is the release-gated target.

Every workspace reloads in place after an engine self-restart, so all windows rebuild from persisted state together. Each workspace reports changed-only screen bounds outside React state; the owned Windows Chrome launch restores valid secondary workspaces on the next cold start and leaves main focused last. Manual secondary closes remove that workspace from the saved open set, while self-restarts keep existing windows in place. A clean engine stop closes each eTape window and any News Reader popup owned by that window; browsers may refuse to close a manually opened fallback window.

At PRE and RTH transitions, SessionClock asks the shared sound engine to play the
selected bundled female-voice recording. These Session Transition Announcements respect
the Session voice toggle, Enable sounds, and volume. Web Locks and a stored ET-date/session token allow
only one audible workspace to announce; without coordination, playback is best
effort per workspace. Opening a workspace mid-session never replays the clip.
The existing weekday-only clock schedule remains the source of these cues.

Dockview owns tabs, activation, native drag-and-drop, merge/split, floating/popout, overflow, and close behavior. A multi-panel group uses Dockview's default tabs above the active Panel Header; a singleton group uses `PanelHeaderTab` as a full-width host for the live `.ledger-header`. `PanelFrame` remains the sole header owner. The host registry is scoped to each Dockview instance, and the inline header fallback exists for standalone panel tests.

Persisted workspaces require `layoutVersion: 8`. `WorkspaceStore` replaces an unmarked or older saved workspace with a blank version-8 workspace and writes that reset immediately. Built-in presets are trusted version-8 layouts. Imported layout payloads must declare version 8 after envelope and shape checks; older or missing versions are rejected as `Invalid layout` and never applied. Hotkey-only imports remain independent.

`monitoring` is a reserved workspace identity, not a replaceable preset. The catalog and New Window modal open it through the named popup target `etape-workspace-monitoring`; rename/delete controls are omitted. A missing Monitoring document is seeded once with four pinned, unassigned charts, a Scanner, and unassigned Stock Info. Existing Monitoring data is kept, and unassigned Monitoring charts show `Waiting for Scanner Sync` without creating a chart or symbol demand until a symbol is typed or linked.

General Layout downloads are structural: they keep panel arrangement, Link Group membership, focused venues, and non-symbol settings, but omit panel symbols, Link Group focused symbols, and cross-window Scanner Source identities. An enabled Monitoring Sync therefore imports paused until a source is selected. Existing saved workspaces and previously downloaded files are not rewritten.

Monitoring Scanner Sync persists its enabled intent and the Scanner Source workspace/panel identity in the Monitoring workspace. Any Scanner header can select the source; AppShell reads the source workspace's persisted sort through workspace-change notifications, so closing its host window does not retarget or stop the relationship, while deleting the source pauses it. Its pure planner retains ranked symbols in their chart slots, fills departed slots from the source's visible sort, and leaves unmatched chart symbols in place when rows are scarce. Turnover, REL VOL, and Short Int are Scanner Source sorts, so Monitoring Sync follows the selected `turnover`, `relVol`, or `shortInterest` ranking automatically. AppShell coalesces successful symbol applications to one batch per second and patches only each chart's symbol; the panel-symbol runtime keeps mounted charts and their settings alive.

Scanner `Vol` and `Turnover` display the latest base daily totals reported by the
snapshot, including valid zeroes and showing `—` when unavailable. `REL VOL` is a
full-day ratio against the mean of up to 50 preceding raw daily bars, so it has no
same-time-of-day adjustment and does not add separate session fields. `SESSION
VOL` displays the matching provider-reported volume for the active pre-market,
RTH, after-hours, or overnight session; it never adds session fields together and
is unavailable when that session block is missing. Scanner's Session Volume
minimum is independent of daily `Vol`, and each threshold has its own K/M
selector; a hidden metric column remains filterable. Scanner metric
column visibility/order is saved per panel and carried
by layout export/import; `SYMBOL` stays first and visible.

Scanner Sound is produced by each unmuted Scanner Panel that remains mounted
in a workspace window, including inactive tabs and background or minimized
windows. The Panel Header exposes a persisted `scannerSoundMuted` toggle;
missing settings default to unmuted, and closing the panel removes its
eligibility without replaying missed hits. The global sound controls remain the
master gate, and multiple eligible panels in one window rely on the shared
sound engine's existing coalescing to emit one cue.

The shell owns the ephemeral cross-window hotkey target coordinator. It listens
only to Dockview user-origin panel activation, seeds a restored active panel
only in the OS-focused window, and republishes the owning panel's group, symbol,
and resolved venue as those contexts change. It clears the owner on panel/window
removal and never persists the target. The injectable channel seam is local to
the UI; it does not change the WebSocket or workspace contracts.
