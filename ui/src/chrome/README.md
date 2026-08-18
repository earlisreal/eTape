# Chrome

Workspace shell, dock layout, settings, controls, and execution surfaces. Inputs: imperative stores plus user actions; outputs: wire commands and panel/controller lifecycle. Children: [execution UI](exec/README.md), [panels](panels/README.md), `controls`. React state stays low-frequency. Test: `npm test`.

The global Top Bar places the browser-derived ET clock and weekday session-transition countdown immediately after connection latency, centers the active hotkey target (Link Group dot, symbol, and venue), and keeps shell actions on the right in every workspace window.

Dockview owns tabs, activation, native drag-and-drop, merge/split, floating/popout, overflow, and close behavior. A multi-panel group uses Dockview's default tabs above the active Panel Header; a singleton group uses `PanelHeaderTab` as a full-width host for the live `.ledger-header`. `PanelFrame` remains the sole header owner. The host registry is scoped to each Dockview instance, and the inline header fallback exists for standalone panel tests.

Persisted workspaces require `layoutVersion: 8`. `WorkspaceStore` replaces an unmarked or older saved workspace with a blank version-8 workspace and writes that reset immediately. Built-in presets are trusted version-8 layouts. Imported layout payloads must declare version 8 after envelope and shape checks; older or missing versions are rejected as `Invalid layout` and never applied. Hotkey-only imports remain independent.

`monitoring` is a reserved workspace identity, not a replaceable preset. The catalog and New Window modal open it through the named browser target `etape-workspace-monitoring`; rename/delete controls are omitted. A missing Monitoring document is seeded once with four pinned, unassigned charts, a Scanner, and unassigned Stock Info. Existing Monitoring data is kept, and unassigned Monitoring charts show `Waiting for Scanner Sync` without creating a chart or symbol demand until a symbol is typed or linked.

General Layout downloads are structural: they keep panel arrangement, Link Group membership, focused venues, and non-symbol settings, but omit panel symbols, Link Group focused symbols, and cross-window Scanner Source identities. An enabled Monitoring Sync therefore imports paused until a source is selected. Existing saved workspaces and previously downloaded files are not rewritten.

Monitoring Scanner Sync persists its enabled intent and the Scanner Source workspace/panel identity in the Monitoring workspace. Any Scanner header can select the source; AppShell reads the source workspace's persisted sort through workspace-change notifications, so closing its host window does not retarget or stop the relationship, while deleting the source pauses it. Its pure planner retains ranked symbols in their chart slots, fills departed slots from the source's visible sort, and leaves unmatched chart symbols in place when rows are scarce. Volume Ratio and Short Int are Scanner Source sorts, so Monitoring Sync follows the selected `volRatio` or `shortInterest` ranking automatically. AppShell coalesces successful symbol applications to one batch per second and patches only each chart's symbol; the panel-symbol runtime keeps mounted charts and their settings alive.

The shell owns the ephemeral cross-window hotkey target coordinator. It listens
only to Dockview user-origin panel activation, seeds a restored active panel
only in the OS-focused window, and republishes the owning panel's group, symbol,
and resolved venue as those contexts change. It clears the owner on panel/window
removal and never persists the target. The injectable channel seam is local to
the UI; it does not change the WebSocket or workspace contracts.
