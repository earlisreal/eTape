# Scanner comparison windows and repeat alerts

Status: needs-info

Brainstorming draft. Accepted decisions below are requirements; open decisions
remain unapproved. This is not authorization to implement the feature.

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
- Startup, reconnect and applying filters establish a silent baseline.
- Missing rolling history means unavailable, with qualification and alerts
  suppressed. Never substitute a shorter comparison window.
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

## Open decisions and investigation

- Simplest history option: record only successful fresh Scanner snapshots for
  all tracked candidates and retained rows. This requires 1/5/60-minute warmup
  for newly tracked symbols and is sampled at polling cadence. Extended-session
  snapshot blocks lack their own exchange timestamp, so observation time must
  be identified honestly. This option is proposed, not yet accepted.
- Existing snapshot batching supports 400 symbols per request and at most
  eight requests per poll. Increasing rank count to 100 fits a single batch
  initially; accumulated rows, errors and shared provider limits still need
  capacity validation. Existing chart history does not universally cover
  candidates; cached Moomoo 1-minute bars require subscriptions, and the current
  Alpaca history path stops 16 minutes before now.
- Rolling history source, startup warmup, timestamp precision and gap handling
  across all candidates and retained rows; do not assume chart subscriptions
  cover the Scanner universe.
- Crossing during cooldown: discard it, or defer notification until cooldown
  expiry if still qualifying?
- Behavior for Top losers, Most active, and a zero percentage threshold.
- Whether repeated hits restore the existing unread/highlight indicator.
- Handling rolling windows across session boundaries and the trading-cycle
  reset, including symbols without a current-session trade.
- Confirm full shared understanding after resolving the decision frontier.

## Validation required for implementation

Check threshold crossings, cooldown boundaries, silent baselines, unavailable
history, session/calendar transitions, startup merging, updated ranking and
Scanner Sync, settings dismissal, and filter persistence migration. Run the
repository's CI-equivalent Windows checklist for the engine/UI contract change.
