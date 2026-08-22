# 07 — Harden Stream parity and the test-only server path

**What to build:** Harden the Workspace Stream into the complete bounded transport contract and expose that same product service and Stream handler through an isolated, loopback-only server build for automated browser testing.

**Blocked by:** 06 — Connect one Workspace end-to-end through Wails Stream.

**Status:** ready-for-agent

- [ ] Lossless frames remain FIFO with no gap, duplicate, silent drop, or reordering at normal, exact-capacity, and overflow boundaries; overflow produces an explicit disconnect rather than hidden loss.
- [ ] Latest-wins keys converge to their newest sequence under load while interleaved lossless frames retain order, every asynchronously sent buffer has immutable ownership, and measured combined eTape/Wails queue capacity and high-water marks remain within declared bounds.
- [ ] With four Workspace Streams active, one renderer stalled for ten seconds remains within declared memory and stale-frame budgets, never blocks the UI Hub or any other Workspace, and recovers or disconnects according to the documented policy.
- [ ] Explicit protocol frames preserve stop and restart meaning, and malformed protocol, unknown Workspace, stale session, mismatched Native Window identity, reload/close races, and late demand are rejected without mutating unrelated state.
- [ ] One hundred WebView reloads and one hundred lifecycle start/stop cycles release handlers, subscriptions, demands, indicators, watchers, and backfill ownership exactly once with no goroutine leak or store write after close under the race detector.
- [ ] A test-only Wails server uses the same services and Stream handler, an isolated temporary profile and identity registry, a loopback random port, and explicit readiness; no server capability or listener enters the packaged product.
- [ ] Playwright covers generated runtime setup, Stream reconnect, snapshot-before-delta on initial attach, reload, and reopened Workspace, plus representative store routing through the Wails server build, while packaged native smoke proves the WebView2 Stream path.
- [ ] Product startup opens no legacy listener, including the historical default port; any temporarily retained legacy adapter is an unstarted branch-local oracle only.
- [ ] Transport, test-server, and end-to-end documentation states the queue policy, cleanup ownership, server limitations, isolated-profile requirement, and authoritative validation commands.
