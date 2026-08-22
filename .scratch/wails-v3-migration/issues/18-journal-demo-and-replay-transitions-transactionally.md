# 18 — Journal demo and replay transitions transactionally

**What to build:** Make demo and replay entry, restart, recovery, and return-live transitions restore the exact prior live desktop by committing a durable phase journal before any mode-changing restart.

**Blocked by:** 09 — Migrate non-execution mutations to typed bindings; 12 — Restore geometry and distinguish restart from crash; 13 — Add verified profile backup and additive migrations; 14 — Centralize global Link Groups.

**Status:** ready-for-agent

- [ ] Before a mode-changing restart, one transaction commits the pre-transition Workspace documents, global Link Group focus, prior mode, target mode, and transition phase.
- [ ] A successful transition acknowledgement means the journal is durable; restart cannot begin while that commit is only queued or debounced.
- [ ] Explicit typed Start Demo, Start Replay, and Go Live operations are the only mode-transition entry points and cannot bypass the journal.
- [ ] Recovery is idempotent at every entry, restart, startup, restore, and return-live phase, including repeated crashes at the same phase.
- [ ] Returning live restores the exact pre-transition Workspaces, documents, open-window intent, and Link Group focus without mixing live and demo/replay state.
- [ ] A failed journal write or invalid journal aborts the transition visibly and leaves the current durable mode state usable; it never falls back to default symbols or an in-memory frontend snapshot.
- [ ] Transition state and recovery diagnostics exclude credentials, symbols, accounts, balances, and other sensitive payloads.
- [ ] Fault-injection tests cover every phase of demo and replay entry, whole-process restart, crash recovery, retry, and return live, proving exact restoration and no mixed state.
- [ ] All automated and acceptance validation uses isolated demo, replay, simulated, paper, or read-only-live profiles and places, modifies, or cancels no real order.
- [ ] Mode-transition, recovery, and operator documentation explains journal phases, durable acknowledgement, safe retry, and failure recovery.
