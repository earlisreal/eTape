# Deferred Position Sizing requires engine custody

Status: accepted — implemented 2026-10-02.

A broker-native stop requires a concrete share quantity, while Deferred
Position Sizing depends on the long position when the sell stop triggers.
These DAY stops therefore use Engine-Held Stop-Limit custody in RTH as well as
pre/postmarket, retaining live account acknowledgement and explicit session
deadlines instead of sending a placeholder quantity to the broker. This
allows placement before an entry fills but provides no broker protection
before activation; a trigger without a long position rejects permanently,
and the stop is independent of any entry order.

Position state is maintained from confirmed broker events and reconciled in
the background so trigger-time sizing adds no broker position-query round trip.
Unreliable position data pauses affected stops until reconciliation and manual
Resume; a trustworthy flat position still rejects permanently.
