# Chrome

Workspace shell, dock layout, settings, controls, and execution surfaces. Inputs: imperative stores plus user actions; outputs: wire commands and panel/controller lifecycle. Children: [execution UI](exec/README.md), [panels](panels/README.md), `controls`. React state stays low-frequency. Test: `npm test`.

The shell owns the ephemeral cross-window hotkey target coordinator. It listens
only to Dockview user-origin panel activation, seeds a restored active panel
only in the OS-focused window, and republishes the owning panel's group, symbol,
and resolved venue as those contexts change. It clears the owner on panel/window
removal and never persists the target. The injectable channel seam is local to
the UI; it does not change the WebSocket or workspace contracts.
