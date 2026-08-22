# 04 — Prove binding, Stream, and server-mode beta semantics

**What to build:** Convert the exact pinned Wails beta assumptions into permanent capability checks, proving that caller identity, binding lifetime, Stream ownership and backpressure, app-wide events, generated bindings, and server testing can support eTape's accepted safety and transport contracts before dependent migration work begins.

**Blocked by:** 02 — Pin Wails and boot a packaged Main shell.

**Status:** ready-for-agent

- [ ] Desktop checks prove whether a binding and Stream can be associated with their owning Native Window; if binding caller identity is unavailable, the accepted opaque Stream-session capability fallback is demonstrated without trusting a JavaScript Workspace identifier.
- [ ] Binding checks cover cancellation and calls still running when shutdown starts, providing evidence for an application-owned admission and in-flight gate rather than relying on Wails cleanup order.
- [ ] Stream checks cover owning-window access in desktop mode, the no-window server case, ordered close on reload and window close, immutable sent-buffer ownership, bounded sends, combined queue limits, and handler lifetime.
- [ ] Event checks confirm beta.11 delivery is app-wide, exercise queue saturation, and prove that targeted, high-frequency, persistence-critical, and order-critical correctness cannot depend on ordinary events.
- [ ] Generated service bindings and models are produced in the committed read-only frontend contract location by the pinned generator.
- [ ] Server-mode checks prove the same binding and Stream APIs needed by Playwright work without a Native Window and can use an isolated test identity registry.
- [ ] Every accepted capability has a focused automated regression check and any beta caveat is documented next to its owning architectural contract.
- [ ] A locked capability mismatch leaves this ticket incomplete and is raised for design revision; generic events, localhost product transport, weakened focus checks, or experimental composition hosting are not substituted silently.
