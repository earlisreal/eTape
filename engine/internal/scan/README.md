# Scanner

Session-aware scanner polling, float/snapshot enrichment, filtering, and UI publication. Request/response scans consume no subscription slots; endpoint rate budgets still apply.

The board is sticky for one trading cycle: post-market movers accumulate with overnight, pre-market, and RTH movers. Entering post-market (normally 16:00 ET) clears the prior cycle. An engine started during RTH fetches pre-market once before merging the current RTH ranking; a failed bootstrap does not block RTH and is retried until successful. Changing filters or ranking mode clears the board and repeats that bootstrap during RTH. Every poll refreshes accumulated rows with one batched market snapshot; temporary snapshot failures retain the prior price, close-based percentage, and cumulative volume.

Test: `go test ./internal/scan`.
