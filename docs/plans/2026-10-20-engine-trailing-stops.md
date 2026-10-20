# Engine-Held Trailing Stops

Status: executing the [approved specification](../../.scratch/engine-trailing-stops/spec.md).

## Scope and sequence

1. Add trailing configuration/state and standalone price/percentage exits to
   the existing execution Core. Cover sizing, reservations, retirement,
   recovery, disarm and symmetric SELL/COVER reduction guards.
2. Add execution-grade Book/candle input and engine-owned demand independent
   of chart mounts; preserve causality, continuity and reference snapshots.
3. Extend Chart Risk Entry presets/conversion and confirmed owned-share
   accounting, retaining initial fixed protection and late-fill cleanup.
4. Wire Go-owned commands/projections and regenerate TypeScript. Add ticket,
   template/hotkey/Deck, chart management and normalization/import behavior.
5. Update guides, run required validation, complete two-axis review, commit,
   integrate main, push and verify full hosted CI.

## Confirmed TDD seams and review

Earl confirmed these seams on 2026-10-20: execution Core commands/events to
order updates and simulated broker outcomes; market-data input to trusted
candle/BBO events; WebSocket command/contracts; and ticket/hotkey/chart controls
to submitted commands and rendered state. Use vertical red/green slices with
real internal collaborators and fake external broker/feed/time boundaries.

Final Standards and Spec reviews compare against approved-spec commit
5b9791b9c48b4ef77d141ed800e8c4feca79fea2, confirmed by Earl.

## Validation

Run focused Go/Vitest tests and regular UI typechecks during implementation.
At completion run full Go/UI suites, race checks, applicable lint/vet/build,
generated-contract drift and relevant browser scenarios. Run git diff --check
and validate documentation links. Follow .github/workflows/ci.yml for full
hosted CI on the pushed main commit. No live orders are authorized.

## Progress

- Core, ordered market-data evidence, risk presets/conversion, wire contract
  and ticket/hotkey/Deck/chart controls are implemented on the task branch.
- Focused regressions cover long/short ratchets, exact touch, frozen quote
  reservations, manual reductions, partial/late fills, restart/flat retirement,
  candle provenance/causality/corrections/timeframe edits and chart controls.
- Full validation and the fixed-base two-axis review are in progress. Initial
  validation exposed Windows sandbox temporary-file rename denials, test focus
  modality assumptions, the new order-type expectation and curated enum parity;
  those are addressed. Wall-clock performance guards are being rerun serially.
