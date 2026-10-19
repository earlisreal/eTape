# TradingView Chart Integration

The bell tool and chart menu open the shared price-alert editor. Amber dashed
levels and bell tags follow chart coordinates imperatively without changing
autoscale; overlapping tags open a chooser. Dragging previews a price and opens
the editor on release; only Save mutates the alert. Paused tags include a pause
indicator, triggered levels disappear, and Alerts panel editing remains available.
Alert tag graphics share the passive pill layer behind native cursor/timer labels;
order controls keep input priority. Bell selection suspends order gestures until
it is cancelled or the creation editor opens. No modifier binding is added.

Lightweight Charts adapter and chart-specific UI integration. Inputs: bar/indicator/drawing stores plus current Free Float and Alpaca borrow classification from the low-frequency Stock Detail store; these values are independent of crosshair/bar updates. Volume is the single local bar-derived Chart Indicator, labeled `Vol` directly below Float; engine indicator values come from the engine store; Volume Profile uses its own captured-print query. Indicator corrections use a monotonic-safe full-data replacement, while tail growth/revision stays incremental. Outputs: imperative series/marker mutations and live legend writes. Visible labels use primary text, hidden labels are muted, and hidden values stay mounted but blank. Preserve stable controller ownership and dispose subscriptions on unmount. Test: `npm test -- tv`.

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

Chart Order Gestures and Risk Entry selection use an Order Preview Line, green
for BUY/COVER and red for SELL/SHORT, at the actual snapped trigger price. Both
show a read-only Preview Price Label without × on the right axis. Bound modifiers
activate feedback without another pointer
move. The native horizontal crosshair and Native Crosshair Price Label hide
during selection, while the vertical crosshair/time label keep normal styling
and follow captured risk drags. Placement or edit completion restores the normal
crosshair. Hover the chart for order details; blockers, immediate-trigger warnings
and unknown outcomes stay visible.
Escape consumes the current modifier press; release modifiers to rearm.

Selected risk draft prices and working orders retain compact right-axis Order
Price Pills. Resting pills share collision spacing; Risk preview labels follow
their actual candidate price with pane-edge bounds and may cover a resting pill.
Pill graphics sit behind the timer and cursor/preview price labels. One pill comes
forward for drag, then hover, then keyboard focus; release restores hover/focus
priority immediately. Passive graphics mirror the original accessible controls,
which retain their existing input targets above the native canvas.
Risk Entry additionally shows a centered vertical red arrow from BUY to the
candidate protective SELL, without a filled or outlined rectangle. Beside the
arrow, whole shares and estimated dollar risk update on mouse movement before
SELL is fixed and remain during draft edits. Completed estimates use the same
quantity cap as submission; invalid/stale sizing shows a placeholder and visible
blocker. The transparent readout stays within the main pane, tracks chart paint
and resize, and passes input through. The moving preview hides outside the main
price pane. Risk Entry retains its navigation lock and Enter submission.
Auto-send applies to completing placement and releasing a moved endpoint of a
completed draft. An unchanged click, incomplete pair, or keyboard draft edit does
not auto-send. Enter and auto-send share route refresh and execution validation;
eligible-print age does not block acceptance. Missing or stale trades leave the
durable pair waiting in the engine; the chart marker's tooltip exposes its
`Waiting for market data` reason without a label over the chart. Only a trusted
recent trade can produce the immediate-trigger warning or activate the entry.
Accepted orders keep waiting for a fresh qualifying trade until normal expiry
or cancellation.
Failed route refreshes and other blockers retain the draft with a visible reason
for Enter or a completed price correction. Busy or unknown outcomes cannot submit
again. Store updates do not retry submissions, and feed/restart pauses retain
manual Resume.
Drag either an editable line in the main pane or its price chip; the nearest
line wins, with a chooser for exact ties. A bound new-order gesture takes
priority over working-line dragging, and an active risk draft owns its input.
The first selected BUY can be adjusted before choosing SELL without completing
or sending the pair. Working-line drags reuse the existing amendment preview
and send one replacement on release; Escape/right-click/focus loss cancel.
Price chips retain arrow-key edits, hover details and existing × behavior.
Nearby chips remain spaced while lines stay at their actual prices. Pending,
unknown and non-editable order states retain their existing restrictions.

Volume Profile is the second local Chart Indicator: its dedicated bounded query reads actual captured prints rather than bar volume. The picker allows one per panel; Inputs configure rows, Value Area and left/right placement, and Style uses the existing color/visibility/line controls. Its imperative legend is range-wide regardless of crosshair, shows Captured volume plus POC/VAH/VAL, and reports Loading, Partial, Stale, empty or unavailable. Hover details include selection, committed freshness, observed endpoints and conservative coverage reasons. Demo and D/W/M clear profile graphics and show unavailable. Hiding, removing or deactivating the panel stops refresh; symbol changes invalidate results immediately.
