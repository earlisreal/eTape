# Protective price calibration, 2026-10-20

Status: implementation evidence for the
[approved plan](../../docs/plans/2026-10-20-protective-stop-spike-filter.md).
No runtime captures, account data or credentials are included here.

## Method

`engine/scripts/calibrate_protective_prices.py` opens supplied archive segments
with SQLite `mode=ro` and `query_only`, retaining normal WAL consistency.
It reads at most 20,000 accepted realtime Last-Eligible prints per selected
symbol, joins actual MD processing records by delivery identity, and orders
prints/books by local receipt, ingress and list index. It does not place
orders, alter subscriptions or modify archive contents.

Three existing local segments supplied active, quiet, sub-dollar and higher
priced observations in regular and extended sessions. The October 16 segment
was unclean; the others were clean, but all reported incomplete attribution
and recording gaps. Treat these as observed samples, not complete market
coverage or proof that a missing quote never arrived.

| Segment date | Eligible prints | Usable prior book at 250 ms | Suspicious observations | Later near price within 100 ms but contrary book | Return with agreeing book within 100 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| 2026-10-20 | 12,770 | 6,527 | 3,449 | 1,213 | 123 |
| 2026-10-19 | 12,503 | 7,237 | 1,237 | 221 | 69 |
| 2026-10-16 | 2,287 | 787 | 484 | 19 | 5 |
| Total | 27,560 | 14,551 | 5,170 | 1,453 | 197 |

These diagnostic counts compare adjacent eligible prints; production compares
against the accepted protection reference. Counts overlap and do not label
reports invalid, establish false-positive rates, or predict a fill. In
particular, a second nearby print frequently coexists with contrary book
evidence; it cannot automatically override that evidence.

This archive proxy checks side-clock presence, connection identity, book
residence and price geometry. It does not replay acknowledged lease revisions
or the production guard's independent side-clock progression/residence checks;
its usable-book count therefore overestimates available production proof.

## Initial engine policy

- Confirmation wait: 100 ms from local frame receipt, never extended by more
  prints or cached evaluations.
- Book residence: at most 250 ms, nonnegative local age, with acknowledged
  current subscription/connection evidence and independent positive side
  clocks. Provider clocks are not subtracted from local wall time.
- Book coherence: positive, non-crossed sides and spread no more than 1% of
  midpoint. Unknown/zero/regressing clocks and broader books are unusable.
- Book price buffer: the greater of one spread and two order-price increments
  ($0.02 at/above $1; $0.0002 below). This is a tolerance floor, not a rule
  about legal transaction-price granularity.
- Jump flag without supporting book: more than the greater of that floor
  and 1% of the accepted protective reference. It flags uncertainty, never
  permanently rejects a genuine gap.
- Nearby-print corroboration: within the greater of that floor and 0.25%
  of the candidate price, using a distinct eligible report in the original
  window and no usable contrary book.

Active-sample median received-book ages were approximately 141-199 ms;
many 95th percentiles were around 278-297 ms. Quiet symbols had much older
books and often little usable coverage. A 250 ms cap deliberately leaves
older evidence unknown rather than asserting its validity. Fixed exits
retain trade corroboration or the bounded adverse fallback in that case.

## SAIQ constraint and regression

The $6.00 fixed protective stop activated at the same local timestamp as the
$5.23 report. The preceding book was $6.30/$6.34, 145 ms older. Another low
arrived at +55 ms while that book contradicted it. A $6.2708 report arrived
at +98 ms; the preceding book was then 243 ms old and its padded range
contained both the earlier $6.34 reference and the recovery.

The real-Core/fake-broker regression preserves that arrival order and the
independent provider/local clocks. It must suppress that crossing when the
book remains usable. Unknown/stale-book controls must preserve protection;
the trace alone does not justify rejecting a crossing when evidence is absent.
There are only 2 ms between the observed recovery and the deadline, so tests
also cover queue delay and late recovery. Historical prevention is conditional
on book validity and processing the evidence within the waiting budget.

Do not tune the book cap solely to make this one trace pass, claim NBBO
authority, or expand the waiting budget. Reassess the internal defaults using
more complete captures and candidate outcome diagnostics as evidence grows.
