# Startup market-data latency

## Goal and scope

Remove unnecessary startup dependencies affecting charts, Scanner, Level 2,
and Time & Sales. Earl authorized implementation on October 7 after reviewing
the purpose of the initial pull pause. Keep provider pacing, subscription/history
quota admission, foreground priority, ticker ordering, and execution safeguards.
Do not add a loading screen or restart a running live engine for verification.

## Current-code evidence

- `request_budget.go` applies a 31-second quiet period to subscription requests
  even though the subscription family has only a local burst guard.
- `opendfeed.go` seeds candle, book, ticker, and quote caches serially. Ticker
  pushes are gated while their seed is pending, so candles can delay Tape too.
- `main.go` waits for sequential optional Alpaca asset loads before all pollers.
- Alpaca history always waits one minute on each production process launch.
- Managed Scanner panels derive warming demand only from admitted board rows;
  a positive REL VOL minimum can prevent its own baseline from being fetched.
- Ladder/Tape render on data revisions without a chart-readiness barrier.

## Decisions and file-level steps

1. In the shared OpenD request seam, exempt subscriptions from initial quiet,
   retain their one-second spacing, and separate static/subscription-quota/history-quota
   local gates while retaining each endpoint's five-second spacing.
2. Add `netx.RestartCooldown` using the existing atomic-file writer. Persist
   each limited attempt before sending it; use a restored timestamp plus the
   conservative provider window. Save observed Alpaca reset/Retry-After too.
   Missing/corrupt state, provider changes, or clock rollback keep the full
   initial wait. Persistence failure prevents an unrecorded limited request.
3. Wire checkpoints beside the database in `main.go`: OpenD scoped to its
   configured address, Alpaca history scoped to a hash of its paper key ID.
   No secrets or market data go into these files. This tracks this engine only.
4. Run claimed cache seeds concurrently within the existing bounded workers.
   Preserve per-subtype deduplication and cache-before-live ticker delivery.
5. Load per-venue Alpaca asset metadata asynchronously and join those workers
   at shutdown, leaving pollers free to start.
6. Wake managed Scanner polling on registration/filter changes. Warm discovery
   candidates with only the REL VOL threshold cleared, through the existing
   shared pool limits; visible board admission retains the original filters.
7. Update affected engine/subsystem READMEs and external API guidance.

## Validation

Regressions cover subscription availability during unknown rolling history,
independent quota gates, a pending candle with responsive book/ticker caches,
ticker seed ordering, durable expired/recent/corrupt/rollback checkpoints,
concurrent checkpoint writes, provider Retry-After persistence, failed saves,
and managed REL VOL warming followed by admission. Run the CI-equivalent
Windows checklist and verify hosted CI after integration into main.

## Rollout, rollback, and limits

Use the normal build/relaunch flow. The first launch without checkpoints retains
the fallback for limited requests; subsequent launches after an idle window skip
it. Rapid restarts wait only the remainder. Unknown-symbol validation still uses
the paced snapshot path and can remain unavailable during a protective cooldown.
OpenD cache readiness, provider response time, subscription quota, and real history
fetches can still delay data; these changes make no fixed wall-clock guarantee.
Other API clients' activity is outside these checkpoints. Checkpoint writes add
a small synchronous filesystem operation to limited sends. Revert the task commit
to restore the old behavior; unused runtime checkpoints may remain beside the DB.
