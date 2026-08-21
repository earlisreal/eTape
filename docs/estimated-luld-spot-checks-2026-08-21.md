# Estimated LULD Manual Spot Checks — 2026-08-21

This is the required public-source comparison record for the initial
display-only implementation. The source review covered the [LULD Plan]
(https://www.luldplan.com/), Nasdaq's [Tier 1 ETP list]
(https://www.nasdaqtrader.com/Trader.aspx?id=ETPSection1), and the [Nasdaq
ETP update notice](https://www.nasdaqtrader.com/TraderNews.aspx?id=ETA2026-34).

An entitled SIP quote stream and a live OpenD session were not available in
this workspace, so no official numerical band was captured. The rows below
are the 20-case manual coverage checklist: each comparison is explicitly
marked `NOT RUN`, with the missing input and expected mismatch reason rather
than inventing a difference. `1 tick = $0.01`; dollar and tick differences are
therefore `n/a` until both feeds are captured in the same session.

| # | Case | Tier/input | Session condition | OpenD input coverage | Estimated − official | Mismatch reason |
|---:|---|---|---|---|---|---|
| 1 | AAPL | T1, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 2 | NVDA | T1, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 3 | SPY | T1 ETP, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 4 | QQQ | T1 ETP, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 5 | IWM | T1 ETP, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 6 | TSLA | T1, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 7 | AMD | T1, >$3 | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 8 | TQQQ | T2, leveraged 3x | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair; multiplier comparison unavailable |
| 9 | SQQQ | T2, leveraged 3x | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair; multiplier comparison unavailable |
| 10 | SOXL | T2, leveraged 3x | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair; multiplier comparison unavailable |
| 11 | UVXY | T2, leveraged 1.5x | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair; multiplier comparison unavailable |
| 12 | ARKK | T2 ETP | RTH mid-session | NOT RUN | n/a / n/a | No live OpenD + SIP pair |
| 13 | $2.50 Tier 2 fixture | T2, <$3 | RTH mid-session | NOT RUN | n/a / n/a | No public live symbol/input captured |
| 14 | $0.60 Tier 2 fixture | T2, <$0.75 | RTH mid-session | NOT RUN | n/a / n/a | No public live symbol/input captured |
| 15 | AAPL | T1 | First five RTH minutes | NOT RUN | n/a / n/a | No OpenD opening prints or SIP band |
| 16 | SPY | T1 ETP | Final 25 RTH minutes | NOT RUN | n/a / n/a | No OpenD close-window prints or SIP band |
| 17 | QQQ | T1 ETP | Scheduled early close | NOT RUN | n/a / n/a | No matching early-close session captured |
| 18 | TSLA | T1 | Post-halt reopening | NOT RUN | n/a / n/a | No public SIP band/reopening capture; local model intentionally warms |
| 19 | TQQQ | T2 leveraged 3x | Post-halt reopening | NOT RUN | n/a / n/a | No public SIP band/reopening capture; local model intentionally warms |
| 20 | Unknown symbol | no registry tier | RTH mid-session | NOT RUN | n/a / n/a | Expected unavailable state; no official comparison is applicable |

Automated deterministic coverage for the same boundaries is in
`engine/internal/md/luld_test.go`, including every percentage bucket, closing
multiplier, early close, leverage multiplier, cent rounding, cadence, quiet
input, warm-up, missing previous close, provider freeze, and reconnect path.
Repeat this record with a live SIP-entitled comparison before making any claim
about numerical agreement; matching values would still not change the
display-only boundary.
