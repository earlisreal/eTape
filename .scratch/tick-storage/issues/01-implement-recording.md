# Implement the approved tick recording plan

Type: task
Status: claimed

Implement [the approved plan](../../../docs/plans/2026-10-08-tick-recording.md).
Recording only; no candle-policy changes, investigation UI/export, or order-flow renderer.

Review baseline: `bfad84a115ab0750d36e5735daa43d44ee0fde7e`.

Approved test seams: queryable SQLite archive and its lifecycle; OpenD protocol
capture/correlation and feed delivery; MD's public feed/seed inputs and evidence
sink; subscription admission against protocol fixtures. Use temporary storage
and simulated protocol traffic; no live orders or live-engine relaunch.

## Comments

- 2026-10-08: implementation requested with `$implement`. Archive persistence,
  exact identifiers/duplicates and bounded queue loss/drain slices pass focused
  tests. OpenD source/index fidelity and existing decode/correlation tests pass.
- 2026-10-09: implementation and subsystem guides supplied. Full engine tests,
  Windows race-short suite, engine build/vet/lint, generated-contract check,
  UI npm ci/lint/tests (1,317)/typecheck/build pass. Final source-reference memory
  change passed dependent subsystem and full engine checks. Crash/WAL, atomic
  rollback, byte/item limits, free-space failure, ET retention, pinned reader,
  Windows failed deletion, source-before-push-drop, exact sequences/BBO clocks,
  PPCB correction attribution and optional BOOK priority/ownership tests pass.
  Synthetic demo smoke passed with isolated profile/port and built UI; no tick
  archive was created. First smoke attempt lacked UI assets; corrected harness.
  Capacity measurements and retained-span limits are in docs/performance.md.
  Standards/spec review and final main integration/hosted CI remain required.
  UI E2E omitted because UI/wire behavior is unchanged; existing full UI/golden
  suite and isolated demo smoke cover compatibility. No live orders/feed restart
  or actual disk fill; storage/transport failures use fixtures.
