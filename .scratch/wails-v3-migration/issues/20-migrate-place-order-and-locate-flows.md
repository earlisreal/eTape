# 20 — Migrate place-order and locate flows

**What to build:** Move order submission, locate requests, and Arm into explicit typed Wails operations so the Order Ticket, Hotkey Deck, and locate surfaces work end to end in simulated and paper modes. Every action must preserve its existing business result and pass through the native focused-window capability immediately before execution.

**Blocked by:** 08 — Introduce generated bindings with read-only queries; 19 — Establish native focus capabilities without execution.

**Status:** ready-for-agent

- [ ] SubmitOrder, RequestLocate, and Arm expose typed inputs and typed accepted, blocked, deferred, or ambiguous outcomes; internal bridge failures remain distinguishable from expected business results.
- [ ] Order Ticket, Hotkey Deck, and locate callers use the generated operations without a generic command or correlation-result path.
- [ ] Hotkey-origin and other symbol-scoped or risk-increasing submissions require the current capability, Panel target, and target revision for the calling Native Window or its proven Stream-session fallback.
- [ ] Native caller association, current window/session generation, and synchronous OS focus are rechecked immediately before simulated or paper execution; a bare Workspace identifier never authorizes an action.
- [ ] Background, minimized, closed, stale, blank, native-dialog-blocked, React-modal-blocked, and editable-control callers fail closed without reaching execution.
- [ ] Button, ticket, and hotkey origins remain distinguishable, and existing DOM key filtering, type-to-load, Deck interaction, and local modal behavior remain intact.
- [ ] Ambiguous order or locate outcomes are never retried automatically, while existing idempotency, risk, broker-acknowledgement, and deferred-result semantics remain intact.
- [ ] A process starts disarmed, restores no execution capability, and observes at most one process-owned initial Arm or auto-unlock request across four restored Workspaces.
- [ ] Existing execution, risk, locate, generated-binding, and affected UI tests pass, including focused-versus-background simulated or paper coverage under the race detector where applicable.
- [ ] No real order is placed, modified, or cancelled while implementing or validating this ticket.
