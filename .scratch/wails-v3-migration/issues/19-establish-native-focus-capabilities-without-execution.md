# 19 — Establish native focus capabilities without execution

**What to build:** Establish a fail-closed native focus and capability contract that can prove which current Window, Stream session, and eligible Panel may act, before any order-execution binding is migrated to consume it.

**Blocked by:** 03 — Establish Native Window, tray, and frameless behavior; 05 — Put engine lifecycle behind admission and drain; 06 — Connect one Workspace end-to-end through Wails Stream; 10 — Make Workspace catalog and Native Window registry canonical.

**Status:** ready-for-agent

- [ ] OS focus and lost-focus events are authoritative for the focused Native Window, with a new generation whenever window or Stream-session identity changes.
- [ ] A WebView may report its active eligible Panel, but receives an opaque capability only when its native window is synchronously focused and its window, Stream session, Panel, and target revision are current.
- [ ] Caller association comes from the Wails calling-window context when available or the proven opaque Stream-session fallback; a JavaScript-provided Workspace identifier is never authoritative.
- [ ] Blank, background, minimized, closed, stale-generation, stale-capability, React-modal-blocked, or otherwise interaction-blocked states fail closed and never fall back to the globally last-clicked Panel.
- [ ] Losing focus to another eTape Workspace or another application immediately revokes the affected capability; close, reload, and shutdown revoke it exactly once.
- [ ] Capabilities and hotkey targets are ephemeral, excluded from persistence, migration, geometry restoration, and crash recovery, and every process launch begins with no target and disarmed.
- [ ] Order hotkeys originate only inside the focused eTape WebView/application path; the desktop host registers no operating-system-wide order shortcut that could execute while another application is active.
- [ ] Any initial Arm or auto-unlock policy has one process-level owner and cannot run once per restored WebView; four restored Workspaces produce at most one request.
- [ ] A public non-executing authorization probe reports accepted or blocked focus outcomes for tests without placing, modifying, or cancelling an order.
- [ ] Coordinator tests cover rapid and out-of-order focus changes, another foreground application, blank and stale targets, minimize/close/reload, modal blocking, caller impersonation attempts, four restored windows, and lifecycle cancellation.
- [ ] Native smoke verifies Alt+Tab and click focus across multiple Workspace windows and another application, with zero capabilities granted to a background or stale caller.
- [ ] Focus, hotkey, lifecycle, and order-safety guides document the capability fields, revocation rules, disarmed startup, session fallback, and the boundary that later execution tickets must enforce.
