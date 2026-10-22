# UI

The Windows Chrome app uses the transparent favicon artwork for its taskbar and
title-bar icons. Favicon and manifest derivatives use the mark's original small-icon
grid so all five bar rows stay separate at 16–40 px, with fixed colors and no
background tile; the native `.exe`/tray icon stays tiled.
Regenerate browser assets with the [icon generator](../scripts/gen-icons.sh).

Choose **Trailing Stop** in the order ticket, or save a `TRAILING_STOP` order
template for a hotkey/Deck button. Configure a positive price/percent distance
or an intraday candle reference, position percentage, daytime sessions and
LIMIT execution recipe. Chart Risk Entry presets offer Fixed (default), Trail
Price, Trail Percent and Candle Trail protection; both chart prices still size
the entry. To replace fixed protection, cancel it and submit a new Trailing Stop
from the ticket or a standalone trail template. Derived stops expose
Edit/Resume/Cancel and cannot be dragged. See the
[execution UI guide](src/chrome/exec/README.md) for run-bound lifetime and data
readiness behavior.

Add **Alerts** through Add Panel to manage engine-owned price alerts across all
workspaces. Its Alerts tab holds saved definitions; History retains every trigger,
including entries whose definition was deleted. Create via the chart's bell tool
or context menu; drag a dashed amber level to preview, then confirm in the shared
exact-price editor. Cancel and Escape discard proposals. Click a symbol in
Alerts or History to load the panel's selected Link Group. Choose a color first;
ungrouped Alerts and Watchlist panels cannot navigate. Watchlist retains its
double-click row navigation and single-click selection.

Popup symbols prefer the most recently used grouped Alerts Panel across open
Workspaces in the same browser profile, including background windows and
inactive tabs. Without a grouped Alerts Panel, they use the source color saved
when the alert was created. Edit and Rearm preserve that color, and trigger
snapshots retain it after definition deletion. Older or ungrouped-source alerts
remain valid; if neither destination exists, choose a color in an Alerts Panel.
View alerts remains available. Symbol activation updates existing group followers
without creating charts, changing venues, or bringing another workspace forward.

The low-rate alert store is separate from live tick rendering. Live burst popups
and Beacon audio use engine connection claims so windows cannot duplicate sound.
Use General → Sounds for Beacon enable/Preview and existing master volume.

Named workspaces are cataloged in engine config (`windows.v1`) by stable UUID; `main` is unnamed and immutable. `monitoring` is the reserved permanent Monitoring Workspace: the catalog opens or reuses its named browser window, and its first missing document is seeded with four unassigned charts, a Scanner, and unassigned Stock Info. Browser Web Locks serialize catalog edits and prevent deletion while a workspace is open.

React/Vite shell around imperative market-data stores and renderers. Wire messages enter `src/wire`, route into `src/data`, then panels/controllers schedule chart or canvas work. The Account panel keeps live Open Orders and the read-only Closed Orders projection in the imperative execution store; the upper tab is session-local while each table's sort preference is persisted. Time & Sales ticks retain the engine-stamped Significant Print level in the imperative tape ring; the separate `md.tape.status` read model feeds read-only settings text. React owns layout/settings, never high-frequency payload state.

Each Scanner Panel persists its own filters with its workspace document and
keeps a separate sticky board, sorting, seen state and sound eligibility in
`ScannerStore`. The engine shares discovery and request budgets across panels.
Closing a workspace pauses its Scanner demand; an enabled Monitoring workspace
keeps following its selected Scanner Source, including when that source window
is closed. Session Volume sorts by current session shares and reports the age
of its shared top-200 discovery page.
The oldest quote timestamp appears inside the Scanner's filter settings.
Clicking, double-clicking or right-clicking a new arrival acknowledges it only
in that Scanner Panel and clears its amber fill and bold text. Single-click
selection keeps a thin outline without an amber fill; double-click loads the
symbol into the linked group.

Grouped charts project open orders into an imperative overlay with compact
`price ×` controls on the right price axis, without B/S prefixes. BUY/COVER uses chart
green and SELL/SHORT chart red across LIMIT, STOP_LIMIT, LIT and draft previews.
Hover the price for shares, side, order type, execution limit and current status;
routine left-edge labels and risk amounts are hidden. Same-side, same-type
orders at one price share a `price (count)` chip and individual-order chooser; opposing
sides stay separate. Nearby chips are spaced without moving their price lines.
Drag a working price or use its arrow keys to adjust; × keeps the existing cancel
target. The overlay follows the execution store; market ticks stay outside React
state. Engine-held orders expose local custody in the tooltip before triggering
and become venue LIMITs only after a fresh eligible print. Chart Order Gestures
retain modifier-click submission. Holding a bound modifier shows a side-colored
Order Preview Line and a read-only Preview Price Label. Preview labels match the
normal crosshair label font, size and left alignment on the price axis, with white
text on a BUY/COVER green or SELL/SHORT red background. Clicking restores the normal
crosshair and uses the existing submitted Order Price Pill. Escape discards the
preview until modifiers are released. Selection hides the native horizontal
crosshair and Native Crosshair Price Label, while vertical/time feedback stays
normal. Blockers and uncertain outcomes remain visible.

Settings → Orders & hotkeys → Add → Chart Risk Entry creates a risk preset and
hotkey. Focus a grouped chart, press the hotkey, then click the buy trigger and
sell stop or drag between them. Only brief setup instructions and actionable
warnings float over the bottom Volume area. The two axis chips expose planned
shares, execution limits, custody, expiry and Enter/Escape shortcuts on hover.
A green BUY or red SELL Order Preview Line follows the proposed risk trigger,
with a read-only Preview Price Label and no × action. Selected draft endpoints
retain their Order Price Pills, including while another price is being selected
or edited. Fixing the pair or finishing an edit restores the normal crosshair.
A centered red arrow connects the BUY and protective SELL trigger levels without
a shaded rectangle. Whole shares and estimated dollar risk update beside it as
SELL follows the mouse and remain through draft review and edits. The estimate
uses cushion-adjusted limits, funding constraints and the completed preview's
submission quantity cap; invalid or stale sizing shows a placeholder and blocker.
The readout stays inside the main price pane, follows price-scale changes and
passes pointer input through. Notional stays hidden. Drag either endpoint or its axis chip to
adjust; Enter submits and Escape or either draft × discards the entire setup.
Right-click, focus loss and context changes cancel.
The global auto-send setting defaults off. When enabled, completing placement or
releasing a moved endpoint of a completed draft submits automatically. Adjusting
only the first BUY, clicking an endpoint without moving it, and keyboard draft
edits do not auto-send. Enter remains available for manual submission.
Both paths refresh the route and revalidate account, position and execution
checks. Missing or stale eligible trades allow the engine to accept the pair;
its chart marker's hover details show `Waiting for market data` until a fresh
trade arrives; no waiting label covers the chart.
A trusted fresh trade that already meets BUY can activate immediately. Otherwise
the accepted entry waits for a qualifying fresh trade until normal expiry or
cancellation. A failed route query or another admission blocker retains the
draft with its reason; Enter or a completed price correction starts another
attempt. Unknown outcomes cannot be resent, and there is no UI retry loop.
Each failed attempt also shows one standard four-second error toast with its
symbol, Execution Venue, and reason, including closed-session blockers. Auto-send
reports after completing SELL or a price correction; manual mode reports on Enter.
Late failure replies still identify the original submission after changing chart
context. Later entry activation/broker rejection or unknown activation shows once
in every open workspace window. Unknown outcomes warn to verify Orders and avoid
resubmitting; a later confirmed rejection is a distinct alert. Repeated deltas,
repaints, and initial/reconnect snapshots do not replay failure toasts. Persistent
draft/order status remains available after dismissal.
Submitted linked markers always request edits on release, showing updated shares
on hover and an above-budget warning after activation. Waiting linked protection
shows planned shares; deferred position sizing shows its percentage until the
trigger determines actual shares. Cancel Protection leaves
filled shares open. Current protection problems appear once per symbol in the
selected Account panel across its tabs, using broker-verified position quantities.
The engine automatically checks unresolved problems; pending/failed checks show
checking/unverified text and retry automatically. Confirmed-flat exposure stays
resolved through restart and later trades, while new late fills remain visible.
Unavailable position data shows `Positions (?)`. No manual Refresh is required.
Cash % and BP % configure loss budgets, excluding fees and execution risk.

Built-in and newly added symbol-bearing panels start unassigned. General Layout downloads preserve workspace structure and non-symbol settings while removing panel and Link Group focused symbols; Monitoring Sync intent remains enabled but imports paused without a portable Scanner Source. Existing saved workspaces and downloaded files are left untouched.

The 10-second chart renders missing weekday 04:00–20:00 ET buckets as client-only synthetic flat bars at the preceding real close, with empty volume. These marked display bars are not stored or included in engine-computed indicators. The local Volume Indicator reads the same display stream, so it omits synthetic bars while retaining real Volume-Only bars; generation pauses at the session close until another real bar arrives.

Children: [application source](src/README.md), [mock engine](mock-engine/README.md), [E2E](e2e/README.md), [test support](test/README.md). Fixtures/assets/generated output excluded from guide leaves. Commands: `npm test`, `npm run typecheck`, `npm run lint`, `npm run e2e`.

Charts offer an opt-in visible-range Volume Profile on intraday timeframes. Add **Volume Profile** from Indicators; settings choose left/right placement (left by default), 1–200 rows (100 default), and 1–100% Value Area (70 default), with total-volume/Value Area colors and POC/VAH/VAL guides. The profile uses retained raw-price captured prints from all selected sessions, labels incomplete evidence Partial and never substitutes candles. A large/timed-out range asks you to zoom in; busy or failed same-range refreshes retain Stale data. Demo and daily/weekly/monthly charts show unavailable. See the [renderer guide](src/render/chart/README.md#volume-profile-indicator).
