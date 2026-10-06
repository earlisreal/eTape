# TradingView Chart Integration

Lightweight Charts adapter and chart-specific UI integration. Inputs: bar/indicator/drawing stores plus current Free Float and Alpaca borrow classification from the low-frequency Stock Detail store; these values are independent of crosshair/bar updates. Volume is the single local bar-derived Chart Indicator, labeled `Vol` directly below Float; other indicator values come from the engine store. Indicator corrections use a monotonic-safe full-data replacement, while tail growth/revision stays incremental. Outputs: imperative series/marker mutations and live legend writes. Visible labels use primary text, hidden labels are muted, and hidden values stay mounted but blank. Preserve stable controller ownership and dispose subscriptions on unmount. Test: `npm test -- tv`.

Chart Order Gestures retain the first click while a missing or expired route
preview refreshes. Submission preserves the clicked trigger price, rechecks
trading status and live account acknowledgement, and allows at most one order
per modifier press. Escape, focus loss, pointer cancellation/leave, and chart
context changes cancel a gesture whose route query is still pending.

An unacknowledged live Engine-Held Stop-Limit chart gesture stays blocked and
announces **Settings → Orders & hotkeys → Review / enable live accounts** in the
preview alongside its eTape custody label. It does not open a chart disclosure,
acknowledge the account, or replay the gesture after enablement; a fresh gesture
is required after Settings shows authoritative acknowledgement status.

LIT chart gestures share modifier bindings with STOP_LIMIT, use the clicked
price as their trigger, preview the current trusted Last-Eligible Price, and
submit the previewed segment deadline. Trigger direction is inverse to
STOP_LIMIT; the pretrigger marker shows the trigger and switches to the child
LIMIT only after durable activation.

A position-% SELL STOP_LIMIT gesture may be staged while flat. The preview keeps
the percentage intent and says **WILL TRIGGER NOW — NO OPEN POSITION** when a
trusted eligible price has already hit the stop and the venue cache is confirmed
flat. Unavailable cache data is shown separately and cannot claim confirmed flat.
The engine resolves shares from its cached position only when the stop triggers;
this is an independent stop order, not a bracket linked to an entry.

Chart Order Gesture and unselected Risk Entry prices use the native crosshair
and price label, green for BUY/COVER and red for SELL/SHORT, snapped to the
actual order tick. Bound modifiers activate feedback without another pointer
move. The moving duplicate line and chip are absent. Hover the chart for order
details; blockers, immediate-trigger warnings and unknown outcomes stay visible.
Escape consumes the current modifier press; release modifiers to rearm.

Selected risk draft prices and working orders retain compact right-axis chips.
Risk Entry additionally shows a centered vertical red arrow from BUY to the
candidate protective SELL, without a filled or outlined rectangle. Beside the
arrow, whole shares and estimated dollar risk update on mouse movement before
SELL is fixed and remain during draft edits. Completed estimates use the same
quantity cap as submission; invalid/stale sizing shows a placeholder and visible
blocker. The transparent readout stays within the main pane, tracks chart paint
and resize, and passes input through. The moving preview hides outside the main
price pane. Risk Entry retains its existing navigation lock and Enter/auto-send
rules; submitted-order marker presentation is unchanged.
Enter and initial auto-send share the submission-time market-data refresh and
validation path. An expired initial preview cannot prevent that refresh. Failed
refreshes and current blockers retain the draft with a visible reason, requiring
an explicit Enter retry. Busy or unknown outcomes cannot submit again.
Drag either an editable line in the main pane or its price chip; the nearest
line wins, with a chooser for exact ties. A bound new-order gesture takes
priority over working-line dragging, and an active risk draft owns its input.
The first selected BUY can be adjusted before choosing SELL without completing
or sending the pair. Working-line drags reuse the existing amendment preview
and send one replacement on release; Escape/right-click/focus loss cancel.
Price chips retain arrow-key edits, hover details and existing × behavior.
Nearby chips remain spaced while lines stay at their actual prices. Pending,
unknown and non-editable order states retain their existing restrictions.
