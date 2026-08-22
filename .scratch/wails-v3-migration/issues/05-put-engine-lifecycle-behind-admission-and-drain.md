# 05 — Put engine lifecycle behind admission and drain

**What to build:** Run the real engine safely under Wails through one concrete lifecycle owner that admits and joins native work, publishes boot state, performs the existing ordered drain once, and never allows a binding or Stream handler to write after storage closes.

**Blocked by:** 01 — Record browser baseline and isolate migration profiles; 03 — Establish Native Window, tray, and frameless behavior; 04 — Prove binding, Stream, and server-mode beta semantics.

**Status:** ready-for-agent

- [ ] Engine startup is asynchronous, returns control to the Wails event loop promptly, and exposes loading, ready, or failure state to Main without starting a legacy product listener.
- [ ] Every binding and Stream entrypoint must acquire the same application-owned admission and in-flight gate before touching engine or storage state.
- [ ] Stop first rejects new work, revokes ephemeral capabilities, cancels long-running work, and joins all admitted bindings and Stream handlers before invoking the existing ordered engine drain and closing storage exactly once.
- [ ] Restart intent returns its binding result before asynchronous shutdown starts, and replacement launch is deferred until post-shutdown has released Wails, single-instance, data-root, and database resources.
- [ ] The lifecycle owner is concrete and not restartable in-process; no generic host interface or parallel engine lifecycle is introduced.
- [ ] Race tests cover new and long-running bindings and Streams against stop, cancellation, drain, store close, and restart initiation, proving no deadlock, goroutine leak, double close, or store write after close.
- [ ] Existing engine shutdown ordering and storage tests remain green with the real engine composed beneath Wails.
- [ ] Lifecycle and operational documentation identifies the single owner, admission boundary, boot-state behavior, and ordered shutdown contract.
