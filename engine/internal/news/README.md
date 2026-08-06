# News

Polls OpenD `Qot_GetSearchNews` (3263), normalizes related US securities, and
upserts stable article IDs to `news.item`. Active UI demand is prioritized, but
a due scanner gets the next slot after three consecutive active requests in
the same 3.1-second, 10-per-30-second quota-controlled lane; publication timestamps retain
`second`/`date`/`unknown` precision.

News remains a polling, in-memory feed: headline/source classification provides
deterministic catalyst scores, but no persistent dedup or price/volume signal.
Test: `go test ./internal/news`.
