# Trailing Stops belong to one Position Cycle and engine run

Status: accepted design 2026-10-20; implementation pending.

Engine-Held Trailing Stops belong to one Position Cycle at their original
Execution Venue and to one engine run: flat, reversal or engine termination
retires untriggered instructions permanently while retaining audit history.
Losing verifiable continuity of the bound Position Cycle also retires its
untriggered trail rather than attaching it to a possibly reopened position.
This deliberately differs from ordinary held Stop-Limit/LIT pause-and-Resume
recovery, preventing a stale trailing exit from applying to a later position
or silently restoring after restart. Graceful shutdown also requests
cancellation of submitted LIMIT children, but restart must still reconcile
unconfirmed broker outcomes because a crash cannot guarantee cancellation.

Trailing-preset intent and its initial fixed fallback share that run-bound
lifetime; ordinary Fixed-mode Chart Risk Entry keeps its existing lifecycle.

See the [approved specification](../../.scratch/engine-trailing-stops/spec.md).
