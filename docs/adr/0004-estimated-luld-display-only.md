# Keep DOM LULD bands display-only estimates

Status: Accepted

eTape will show an Estimated LULD Band in the DOM ladder only when locally
available Moomoo tick input and a dated checked-in tier registry support it.
It will not buy SIP data, call the result an official LULD price band or
trading state, or use it to gate, alter, or reject an order.

Moomoo suspension and security-status fields are provider health signals, not
official LULD state. An affirmative abnormal provider signal or transport
interruption freezes the last local estimate with a clear warning; it never
declares a Limit State, Straddle State, or Trading Pause.

This preserves a useful trading aid without implying that a non-consolidated
feed has regulatory authority.
