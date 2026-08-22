# 21 — Migrate order management and emergency controls

**What to build:** Move cancel, replace, flatten, simulated-balance reset, Kill Switch, and Disarm onto typed Wails operations so every remaining execution and emergency control uses the native focus policy while preserving the existing risk-reducing escape paths.

**Blocked by:** 20 — Migrate place-order and locate flows.

**Status:** ready-for-agent

- [ ] CancelOrder, ReplaceOrder, Flatten, ResetBalance, KillSwitch, and Disarm expose explicit typed inputs and business outcomes through generated bindings.
- [ ] Every Account, order-management, Order Ticket, Hotkey Deck, and related caller uses those typed operations; no execution caller relies on the generic command transport.
- [ ] Cancel and Replace require a currently focused eTape session plus the canonical order identity and revision, and reject stale order context immediately before execution.
- [ ] Symbol-scoped Flatten and any risk-increasing management action require the current focused Panel capability and target revision according to the established risk policy.
- [ ] Disarm and Kill Switch remain reachable from a focused eTape Native Window when no symbol Panel is active, while background, minimized, closed, stale, or interaction-blocked windows remain rejected.
- [ ] Existing accepted, blocked, ambiguous, idempotent, gate, broker-acknowledgement, and deferred-result behavior is preserved, with no automatic retry of an ambiguous result.
- [ ] Rapid and out-of-order focus changes, Alt+Tab to another application, window close/minimize, stale capability and generation, blank Workspace, native dialog, React modal, and editable controls produce zero background or stale simulated or paper commands.
- [ ] A one-thousand-transition four-window adversarial focus run records zero unauthorized execution and no more than one initial Arm request.
- [ ] Existing execution/risk suites, affected UI tests, generated-binding checks, and race-enabled service/coordinator tests pass.
- [ ] Execution and order-safety documentation reflects the typed methods and risk-based authorization rules, and no real order is placed, modified, or cancelled during validation.
