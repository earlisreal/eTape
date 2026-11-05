# Sessions

US Eastern pre-market, RTH, post-market, and closed classification. The embedded
NYSE date calendar includes 1971+ recurring holiday rules, historical election
closures through 1980, exceptional closures, MLK Day from 1998, and Juneteenth
from 2022. Saturday New Year's Day does not close the preceding Friday. Current
recurring early closes and offline, DST-correct `Schedule`, `IsTradingDay` and
adjacent-session helpers support chart/history and Screener coverage. Older
exceptional closures must be represented so complete historical ranges are not
mistaken for missing data. The [XNYS calendar reference](https://github.com/gerrymanoim/exchange_calendars/blob/master/exchange_calendars/exchange_calendar_xnys.py)
documents these historical dates and rule boundaries. Unknown future emergency
closures may be learned by one empty history fetch. Test: `go test ./internal/session`.
