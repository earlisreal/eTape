# Engine pre-live checklist

Open decisions and gaps in the engine backend (all 6 plans merged + pushed to
`origin/main` @ `1a8758e`, 2026-07-07) that should be resolved before wiring live
venues or relying on the scanner/news pollers in a real trading session. Found
across Plan 6's task reviews and the final whole-branch review — see
`docs/superpowers/plans/2026-07-06-engine-uihub-pollers-wiring.md` and its
execution ledger for full detail on each item.

## Design decisions needed

- [ ] **`exec.Core.Recover`'s broker `Snapshot` calls run before uihub starts
      listening.** In `cmd/etape/main.go`, a misconfigured or unreachable live
      TradeZero/Alpaca venue can delay "UI shows connecting" states by up to
      ~10-20s (bounded by each venue's own HTTP client timeout) — for a
      *different* external dependency (the broker, not OpenD) than the
      boot-order invariant explicitly names ("uihub listens before OpenD is
      dialed").
      **Why:** this is the plan's own literal prescribed boot order (exec
      fully recovers before uihub starts), not a bug — reordering has its own
      downside (uihub could then serve pre-reconcile/stale exec state to a
      freshly-connected UI).
      **Decide:** is the current bounded delay acceptable, or should boot
      order change — and if so, how should the UI handle the "uihub up before
      exec reconciled" window (e.g. a reconciling badge)?

- [ ] **`--focus` symbols never reach the news poller.**
      `cmd/etape/main.go`'s `symbols` closure passed to `startPollers` only
      returns `cfg.Feed.Watchlist`; `config.News.FocusedMs` is defined but
      never used, and the news poller doesn't distinguish focused symbols
      from the rest of the watchlist.
      **Decide:** is focused-symbol-priority news polling needed before going
      live, or is watchlist-only polling acceptable for v1?

## Worth confirming before live

- [ ] **Positions never evict on close (Qty=0) in the uihub mirror.**
      `internal/uihub/mirror.go`'s `exec.positions` map is keyed
      `venue|symbol` and never deletes an entry, so a flattened/closed
      position could linger as a stale zero-qty row in the UI's positions
      panel — *if* `exec.Core` ever actually emits a zero-qty
      `PositionUpdate` in practice (unconfirmed either way).
      **Action:** check whether `exec.Core`'s reconcile/fold logic can
      produce a zero-qty `PositionUpdate`; if so, add eviction to the mirror
      before relying on the positions panel during a live session with
      round-trip (open-then-close) trades.

## Low-priority tracked debt (not blocking, but real)

- [ ] News poller's dedup seen-set has no cap or reset
      (`internal/news/news.go`'s `p.seen`), unlike the scan poller's per-ET-day
      reset — grows unboundedly over a long-running session. Low practical
      risk given news volume, but worth a bound given the project's
      stability-first, long-session priorities.
- [ ] No automated CI enforcement. No `.github/workflows` exists —
      `make gen-ts-check`, `go test -race`, `golangci-lint run` are all run by
      hand. The tygo "drift fails the build" headline deliverable is only
      enforced if someone remembers to run it.
- [ ] `wsmsg.go` ↔ `tygo.yaml` frontmatter has no automated parity check. A
      future field added to one without the other silently drifts — no
      compiler or CI catch (`internal/uihub/wsmsg/wsmsg.go` +
      `engine/tygo.yaml`, Task 4's hand-declared-frontmatter tradeoff for
      tygo's `kind` discriminant).

## Related standing context (not new, already governing)

- Alpaca paper matching stalls at RTH (`docs/2026-07-06-venue-latency-benchmark.md`)
  — any live smoke test of the new uihub/pollers stack during RTH shouldn't
  assume prompt paper fills.
- CLAUDE.md's standing safety rule: never place/modify/cancel real TradeZero
  orders without Earl's explicit say-so in the running conversation —
  unchanged by this plan, still governs any live wiring of the broker factory
  built in Task 14.
