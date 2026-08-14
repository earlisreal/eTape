# Chrome

Workspace shell, dock layout, settings, controls, and execution surfaces. Inputs: imperative stores plus user actions; outputs: wire commands and panel/controller lifecycle. Children: [execution UI](exec/README.md), [panels](panels/README.md), `controls`. React state stays low-frequency. Test: `npm test`.

Dockview owns tabs, activation, native drag-and-drop, merge/split, floating/popout, overflow, and close behavior. A multi-panel group uses Dockview's default tabs above the active Panel Header; a singleton group uses `PanelHeaderTab` as a full-width host for the live `.ledger-header`. `PanelFrame` remains the sole header owner. The host registry is scoped to each Dockview instance, and the inline header fallback exists for standalone panel tests.

Persisted workspaces require `layoutVersion: 8`. `WorkspaceStore` replaces an unmarked or older saved workspace with a blank version-8 workspace and writes that reset immediately. Built-in presets are trusted version-8 layouts. Imported layout payloads must declare version 8 after envelope and shape checks; older or missing versions are rejected as `Invalid layout` and never applied. Hotkey-only imports remain independent.

The shell owns the ephemeral cross-window hotkey target coordinator. It listens
only to Dockview user-origin panel activation, seeds a restored active panel
only in the OS-focused window, and republishes the owning panel's group, symbol,
and resolved venue as those contexts change. It clears the owner on panel/window
removal and never persists the target. The injectable channel seam is local to
the UI; it does not change the WebSocket or workspace contracts.
