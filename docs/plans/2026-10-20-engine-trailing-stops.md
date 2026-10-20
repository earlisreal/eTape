# Engine-Held Trailing Stops

Status: implemented the [approved specification](../../.scratch/engine-trailing-stops/spec.md).
Local validation and independent review are complete. Full hosted CI passed on
implementation commit `7c5a8ed2`; see the hosted validation record below.

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
- Local validation: full Go suite (serial), full short race suite, final focused
  execution race suite, vet, golangci-lint, engine build, generated-contract
  drift, UI typecheck/lint/build and full UI tests (1,436) pass. Documentation
  links and git diff --check pass. Windows sandbox rename restrictions required
  running the affected checks outside the sandbox; performance checks ran
  serially. No required local check was skipped.
- Browser chart layout/overlay, light/dark risk controls and narrow multipane
  harness pass. Optional broad Playwright: 12 pass, 22 fail, one closed-session
  skip. An isolated unchanged approved-spec checkout reproduced 21 failures
  (existing picker/selectors/import fixtures and market-session assumptions).
  The reconnect case passes from a fresh workspace. Repeating it in the same
  demo run passes once, then fails setup because the named workspace already
  has its preset applied (the test still waits for the Trading preset card).
  The
  temporary baseline worktree was archived; its dependency junction cleanup
  required restoring local UI dependencies with npm ci, subsequently verified.

## Standards review

The independent review found no documented-standard breaches. One P3 judgment
call identified duplicated directional tick rounding; both stop and quote-limit
paths now use the same small helper. The finding is resolved.

## Spec review

Seven confirmed findings were resolved with public-seam regressions: nested
observational snapshot accounting; ownership reductions preceding conversion;
missing Orders-row Edit before a candle price exists; reverse position/fill
ordering; stale owner cumulative fills; terminal exits omitted from open-order
snapshots; and terminal owners omitted after late fills. The highest severity
was P1. The independent reviewer confirmed all reported issues resolved.

Attribution uncertainty preserves an existing LIMIT and reservation while
blocking additional exits. Combined order/position reconciliation applies
ownership once, rejects stale executions, and exposes late fills discovered
during reconciliation for manual recovery. Waiting Candle Trail edits retain
fresh-print validation without requiring an initial completed reference.

Review totals: Standards one P3 judgment call resolved, zero hard violations;
Spec seven findings resolved, highest P1. No confirmed findings remain open.

## Hosted validation

The first implementation run passed UI lint/tests/build. Windows reproduced
the baseline recording-capacity timing failure: commit lag was 1,107ms against
a 1,000ms guard, versus 1,010ms on the approved-spec baseline; neither lost data.
Windows CI now runs the full Go suite with package parallelism one, matching
the passing local full-suite command and preventing simultaneous synthetic-feed
and recording-capacity load generators. No test or threshold was removed.
The pushed follow-up commit must pass the complete hosted workflow before
handoff, including Linux full/race/vet/lint/contracts, Windows full and UI.

The [full hosted run](https://github.com/earlisreal/eTape/actions/runs/39417619999)
passed all three jobs on `7c5a8ed2331ad2cb97537e1a515090e3b2478930`:
Linux engine full/race/vet/lint/contracts, Windows full engine tests, and UI
lint/tests/build. The Windows README checklist now uses the same serial command.
The final validation-record/checklist commit changes prose only; its hosted
rerun is not required for handoff under the documentation exception in AGENTS.md.
