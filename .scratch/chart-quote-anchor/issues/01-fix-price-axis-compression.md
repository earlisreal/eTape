# Preserve the 10-second chart price reference

Type: task
Status: resolved

## Reproduction

The November 12 CRMN recording shows the 06:07:50 ET candle temporarily flat
at $1.16 while nearby candles traded around $3.95. The prior daily close was
$1.16. The saved 06:07:50 candle opened at $3.9409 and stayed near that price.
The local tick archive lacks raw events for the session.

A [market-data core regression test](../../../engine/internal/md/eligibility_test.go)
reproduces the same move with a late basic quote at $1.16, a volume-only
print, and an eligible print in the same 10-second bucket.

## Fix

The [tick aggregator](../../../engine/internal/md/tickagg.go) uses a basic quote
as a fallback until an eligible trade or trade bar provides a reference price.
A later quote can resolve a pending bucket using the established close. The
first Last-Eligible Print in each bucket sets its open and initial close.
