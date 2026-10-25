# Investigate Windows recorder capacity failures

Type: task
Status: ready-for-agent

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

## Comments

- Temporary hosted CPU, blocking, mutex and runtime traces narrow the remote
  reproduction to the two enabled comparison modes. The original full suite
  still runs and gates CI. Remove temporary diagnostics after diagnosis.
