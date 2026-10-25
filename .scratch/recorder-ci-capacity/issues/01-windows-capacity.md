# Investigate Windows recorder capacity failures

Type: task
Status: resolved

Earl requested investigation on 2026-10-25 after the WebSocket security fix
passed its affected tests but full Windows CI failed three times in
`TestRecordingLoadComparison`.

## Reproduction and evidence

- [Failing CI run](https://github.com/earlisreal/eTape/actions/runs/40179321689)
  at `9b12f956`: attempts failed at 1,095 ms commit lag; archive drain timeout;
  then 3,659 ms recording-only lag and a rejected profile-mode source frame.
- Previous main `d7aa8433` passed on the same Windows runner image
  `20261021.278.1`. Failed attempts used westus2 and centralus; the passing
  baseline used eastus. Region alone is not established as the cause.
- Native Windows isolated comparisons passed normally and with one Go CPU:
  profile-mode maximum commit lag was 246 ms and 241 ms, with no loss.
- The entire uncached native Windows suite also passed with
  `go test -p 1 -count=1 ./...`, including the recorder package in 35.6 seconds.
- Run the actual failure path with `cd engine` then `go test -p 1 ./...`.
  The unchanged workload is 50 symbols, 500 prints/second for ten seconds,
  followed by a 500-print burst, with four profile charts at 1 Hz.
- Keep the one-second lag limit, loss checks, workload and full CI gate intact.

## Ranked hypotheses and probes

1. Hosted disk flushes stall the writer. A runtime trace should show file
   syscall time dominating recorder stalls, including recording-only mode.
2. Fixture timing produces catch-up bursts. A runtime trace should show delayed
   producer timers followed by compressed source submissions.
3. Profile readers delay the writer. Lock profiles should show recorder gate
   waits, and recording-only mode should avoid them; its failure weakens this
   explanation.

## Answer

SQLite's automatic checkpoints were doing expensive disk flushes inside archive
commits. Archive connections now disable them through the driver's connection
settings, including replacement connections. Synchronous FULL and the existing
rotation, seal, recovery, physical-cap and free-space checks remain in place.

The new regression failed before the fix and passed afterward for both initial
and replacement connections. It checks that a large live commit stays in WAL,
FULL durability is configured, committed data is readable, and explicit
maintenance truncates WAL.

[Hosted verification](https://github.com/earlisreal/eTape/actions/runs/40183405789)
at `7c962d2d` passed Windows, Linux and UI CI. The actual diagnostic benchmark
also passed: recording/profile maximum lag was 247/248 ms, with no loss,
40 profile requests, 20 populated results, 20 Busy responses and no timeouts.
The workload and one-second limit are unchanged.

Local validation passed recorder/UI-hub tests, race tests for tickstore, MD and
UI-hub integration, vet, lint and application build. The optional 1 MiB rotation
stress also passed with 555 ms maximum lag and no loss. Temporary profiling
steps are removed; the CI workflow is restored exactly to its prior full gate.

## Comments

- Temporary hosted CPU, blocking, mutex and runtime traces narrow the remote
  reproduction to the two enabled comparison modes. The original full suite
  still runs and gates CI. Remove temporary diagnostics after diagnosis.
- Hosted diagnostic run `40181550677` captured the same failure: profile-mode
  maximum commit lag was 3,036 ms with zero loss. Its independent full suite
  passed; the diagnostic step permits failure, so its success conclusion alone
  does not establish a passing benchmark.
- The failing trace accumulated 9.45 seconds in `FlushFileBuffers`; the CPU
  profile attributed 7.39 seconds to SQLite checkpoint stacks, including
  6.34 seconds inside `Tx.Commit`. This supports the disk-checkpoint hypothesis.
- A fast regression test reproduced the mechanism: a 1,100-page WAL write
  grew the main database from 4,096 to 4,517,888 bytes during its commit.
  The fix disables automatic checkpoints on archive connections and retains
  synchronous FULL plus the existing bounded rotation/seal/recovery checkpoints.
- The same regression caught a one-time PRAGMA setup being lost when the SQL
  pool replaced a connection. Configure the existing driver through its DSN so
  both initial and replacement connections preserve the archive policy.
