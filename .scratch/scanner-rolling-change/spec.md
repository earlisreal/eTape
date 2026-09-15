# Scanner comparison windows and repeat alerts

Status: needs-info

Product decisions are accepted; awaiting final shared-understanding confirmation.
This is not authorization to implement the feature. Technical checks below
belong to implementation planning, not silently relaxed requirements.

## Accepted decisions

- Compare percentage change using a fixed dropdown: Previous close, 1 min ago,
  5 min ago, 1 hour ago. Default to Previous close; label the relationship
  "versus". Time durations mean Rolling Change, not completed candles.
- The selected comparison drives the displayed percentage and gainers/losers
  ranking as well as the percentage filter. Label the percentage column to
  identify its comparison window. Scanner Sync follows the selected ranking.
- Scanner Previous Close is the regular-session close preceding the trading
  cycle. Monday after-hours belongs to Tuesday's cycle: Monday after-hours,
  overnight, Tuesday premarket and Tuesday RTH use Monday's regular close.
  Use the exchange trading calendar, including holidays and early closes.
- Existing rows can alert again: a below-threshold to qualifying crossing
  alerts; remaining above the threshold does not. Dropping below rearms it.
  Apply this rule to Previous close as well as rolling comparisons.
- Enforce at least 60 seconds between alerts for a symbol.
- Discard crossings during cooldown; never queue a delayed alert. Remaining
  qualified when cooldown expires does not alert. Another fresh crossing is
  required after cooldown.
- Startup, reconnect and applying filters establish a silent baseline.
- Missing rolling history means unavailable, with qualification and alerts
  suppressed. Never substitute a shorter comparison window.
- Build rolling history from successful fresh Scanner snapshot observations
  for tracked candidates and retained rows. Newly tracked symbols need the
  selected full 1/5/60-minute window, regardless of engine uptime. Display a
  visible warming-up state. No historical backfill in the first version.
- Rolling Change uses sampled prices at Scanner polling cadence, not exact
  tick-time prices. Do not represent extended-session polling observation
  timestamps as exchange trade timestamps.
- Continuous rolling history crosses session boundaries: at 09:32 ET, a
  five-minute comparison can use 09:27 premarket observations. A session change
  alone neither clears rolling history nor generates an alert. Actual data
  gaps suppress alerts; recovery establishes a fresh silent baseline.
- Mirror directional crossings for Top losers: reaching a loss of at least
  the configured magnitude qualifies. Most active retains new-arrival alerts.
  At a zero percentage threshold, use new-arrival alerts only.
- Repeat alerts restore the unread/highlight state and use the same sound
  and visual treatment as a new hit. Never select the row automatically.
- On startup, merge earlier-session candidates from the same trading cycle
  with the current session's candidates. During premarket this includes
  after-hours, overnight and premarket; refresh prices before filtering.
  Ongoing discovery polls the current session and refreshes retained rows.
- Request 100 candidates per ranking consistently, subject to verification of
  snapshot refresh capacity. This is a discovery count, not a display limit.
- Clicking outside settings or pressing Escape closes settings and discards
  unapplied edits. Apply remains explicit.

## Existing behavior and constraints

- Filters are engine-global; panel sorting is local. Preserve this scope.
- The board retains admitted symbols after they cease to qualify. The current
  cycle resets on entry to after-hours; applying filters also resets the board.
- UI alerts currently come from new appearances in scanner.rank; scanner.hit
  is ignored. A backend-only change to hit emission cannot deliver repeats.
- Rank endpoints discover top session movers, not all US stocks or all rolling
  movers. Broader counts do not guarantee market-wide rolling discovery.
- Snapshot failures currently retain old row values. Those retained values
  cannot be treated as fresh observations for the new rolling calculation.

## Verified provider observations

Read-only OpenD requests on 2026-09-14/15:

- At about 18:03 ET Monday, active after-hours and inactive premarket/overnight
  endpoints all returned populated rankings.
- At 03:57 ET Tuesday, after-hours ranking still returned results.
- At 04:03 ET Tuesday, its top five matched the 03:57 results, while premarket
  returned fresh prices. VEEA: after-hours $4.20, premarket $4.039; premarket
  +76.401% used the $2.29 regular close.
- This establishes retention across this observed transition, not a provider
  guarantee for every day. Inactive ranking fields may refer to different
  sessions: close_price did not always match the stored percentage's baseline.
- Rank requests expose count/offset but no historical date selector.

Sources: [Premarket rank](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-pre-market-rank.html),
[After-hours rank](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-after-hours-rank.html).

## Technical checks for implementation planning

- [Live capacity verification](verification.md) passed for 100 per ranking and
  merged startup snapshot batches. It also found missing current-session prices
  and unbounded aggregate retry rates. Freshness gating and paced/bounded shared
  snapshot work are required before shipping; the healthy probe is not blanket
  approval of the current retry behavior.
- A subsequent bounded premarket probe passed at one-second polling and
  observed additional price changes compared with two-second sampling. A
  one-second target is a supported proposal, not yet an accepted cadence
  change; it requires shared pacing and slower refresh under increased batch
  load. The existing timer rounds default three-second RTH polling to roughly
  four seconds and must be corrected before promising precise session cadence.
- Existing snapshot batching supports 400 symbols per request and at most
  eight requests per poll. Increasing rank count to 100 fits a single batch
  initially; accumulated rows, errors and shared provider limits still need
  capacity validation. Rank count does not bound the accumulated board size.
- Set a bounded baseline-sample tolerance and gap detection policy consistent
  with observed polling cadence. Never bridge a known feed interruption or
  append retained stale values as fresh observations. A successful unchanged
  quote must be distinguished from a failed refresh.
- Verify current-session price availability and baseline dating for bootstrap
  candidates, especially symbols with no current-session trade. A historical
  session price must not masquerade as a current-session observation.
- Preserve rolling observations across session changes independently of the
  existing board's trading-cycle reset. A baseline rollover must not fabricate
  a Previous close price-movement alert.
- Existing chart history does not universally cover candidates; cached Moomoo
  minute bars require subscriptions and the current Alpaca history path ends
  16 minutes before now. The accepted sampled-history design avoids depending
  on those sources for immediate rolling readiness.

## Acceptance examples

- With minimum gain 5%, 4% -> 6% alerts, while 6% -> 10% does not. A later
  3% -> 6% alerts again once the 60-second cooldown has elapsed.
- A crossing at 30 seconds after the last alert is discarded. Still being
  above 5% at 60 seconds does not replay it; a later fresh crossing can alert.
- With minimum loss 5%, -4% -> -6% alerts and -6% -> -10% does not; recovery
  to -3% rearms the symbol for a later qualifying decline.
- An already-highlighted or already-read row can produce a repeat hit. Reading
  a row does not itself rearm its threshold or reset its cooldown.
- A newly tracked symbol in one-hour mode remains unavailable while it has
  only 20 minutes of observations. Never label its 20-minute change as one hour.
- A valid five-minute window can span the premarket/RTH boundary. A known data
  gap cannot be replaced by fabricated quiet-price observations.
- A retained row remains visible after falling below the minimum; its next
  eligible crossing can alert without removing and reinserting the symbol.
- Outside click and Escape dismiss changed drafts without sending an Apply.

## Validation required for implementation

Check threshold crossings, cooldown boundaries, silent baselines, unavailable
history, session/calendar transitions, startup merging, updated ranking and
Scanner Sync, settings dismissal, and filter persistence migration. Run the
repository's CI-equivalent Windows checklist for the engine/UI contract change.
