# Chart Risk Entry position adds

Status: implemented — approved and implementation requested on 2026-11-05.

Allow another Chart Risk Entry on an existing long position protected by a
healthy Chart Risk Entry stop at the same Execution Venue and symbol. Keep one
shared stop for the entire managed position and one pending entry/add, including
partially filled buying. Reject unrelated working orders, incomplete coverage,
uncertain or triggered protection, shorts, and arbitrary broker-native stops.
Adds require stop-relative Limit Cushion execution. A pending add prevents
execution pricing/cushion edits; cancel it first to revise that sizing basis.

Retain the two-point gesture. BUY selects the additional entry; SELL selects the
proposed shared stop. Each preset supplies a separate risk budget for the added
shares, using the existing protection's Sell Limit Cushion and Protection Mode.
SELL must be below BUY and at or above the current shared stop. Existing
position/value/funding gates and the reviewed quantity ceiling still apply.

The existing stop remains unchanged until the first confirmed add fill. Then
tighten it to the higher of its current price and the selected SELL, and grow
coverage with actual fills. Preserve trailing configuration, watermark,
reference, and progress. Never automatically increase the approved add quantity.
Preview existing, added and combined shares, and the shared stop change.

Canceling an unfilled add leaves existing protection unchanged. Canceling a
partially filled add leaves all acquired shares protected. A shared-stop trigger
cancels all remaining buying; protect late fills with the existing durable
cleanup mechanism. Confirmed partial sales reduce shared coverage exactly once.
Cancel Protection cancels the whole strategy without flattening. Preserve
existing session, restart, uncertainty, Disarm and day-loss behavior.

No live orders are authorized by this implementation task.
