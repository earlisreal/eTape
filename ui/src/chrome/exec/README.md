# Execution UI

Order ticket, hotkeys, sizing, venue selection, and arm/disarm presentation.
Inputs: user intent plus account/position stores; outputs: typed wire commands.
UI never bypasses engine gates. Grouped panels resolve only their Link Group's
persisted venue; pinned panels prompt for a Link Group and cannot submit. A
paper-to-live group switch asks for confirmation, while existing orders remain
owned by their original venue and are called out until they finish. Account
panels publish panel-scoped account demand so the engine polls only displayed
paper accounts in addition to its live risk venues. The Account strip exposes
the single broker/calculated Day P&L, eTape cycle Realized P&L, source label,
and stale timestamp. Test: `npm test -- exec`.

Dollar, Cash %, Buying Power %, Shares, and Position sizing use the selected
venue's live account and position data; Cash % uses the same available cash
shown by the Account panel.

Position-% SELL STOP_LIMIT templates defer share sizing through Chart Order
Gestures, hotkeys and Deck Buttons. Their DAY held parent shows percentage
intent before trigger and the resolved shares after activation; a ready flat
cache rejects on trigger with a symbol/venue/reason toast. An unready cache is
shown as unavailable and prevents admission or Resume. The manual Order Ticket
keeps its existing immediate sizing behavior.

The order ticket is optional for hotkey execution. A revisioned, in-memory
`BroadcastChannel` target follows the most recently user-activated Dockview panel
across open windows and carries its owner window, panel id, link group, linked symbol,
and resolved venue. A focused window may seed its restored active panel at startup;
programmatic restores, window focus, top-bar clicks, and modals do not retarget it.
Group, symbol, venue, panel removal, and normal window close updates are coordinated,
but the target is never persisted across a full restart. The top-bar cue is read-only;
it is blocked for no target, an ungrouped panel, a missing symbol, or a missing venue.

Place, Cancel Last, and Cancel All Focused require a grouped target; focused cancels
also require its symbol. Scoped bindings pause silently in modals and editable fields,
and OS key-repeat is consumed. Kill Switch and Cancel All Everything remain available
without a target, while disarmed, and during modal/editor focus. Arming, quote/pre-check
validation, venue fallback, engine risk gates, and sounds are unchanged. Action-template
Cancel Last and Cancel All show immediate request feedback and aggregate blocked or
ambiguous outcomes; ordinary Account-panel cancellation remains unchanged.

Action Templates own the saved order recipe, hotkey, and optional Deck Button
color. `OrderConfig.hotkeyDeck` owns the normalized, non-empty ordered Deck
Rows and the global Hotkey Label Visibility preference; Settings stages both
alongside template edits and persists them together. Legacy `deck` flags
migrate into one row, then remain only as a compatibility membership
projection. Hotkeys exports carry the Deck Layout but never `activeVenue`, and
imports regenerate template ids before remapping row references. Deck Button
clicks still use the shared `fireTemplate` path with `gateArm: false`; engine
arm and risk gates remain authoritative. Order configuration changes are
rebroadcast by key so open workspace windows reload templates, hotkeys, venue,
and safety preferences together.

Action Templates may carry a dollar or percent Limit Cushion for STOP_LIMIT or
LIT; the final limit is directionally rounded to the venue tick and revalidated
by Core. For LIT, the source price is the trigger and sizing uses the resulting
limit. An exact one- or two-modifier Chart Order Gesture can bind to one
STOP_LIMIT or LIT template, with bindings shared between the two types.
Hotkeys, Deck, ticket, and chart entry use the same route preview and submit
path. Engine-held orders are shown as having no broker order before activation;
only a fresh Last-Eligible print submits the linked venue LIMIT child. UI
preview is not execution authority and cannot silently change the resolved
Link Group venue.

Adding or changing a Chart Order Gesture opens a Settings disclosure; continuing
stages the binding until Save. Bound templates expose **Review / enable live
accounts** so each live account can be acknowledged separately. STOP_LIMIT and
LIT acknowledgements are independent; account enablement applies across all
order-entry methods and appears enabled only after ExecStore receives
authoritative venue status. Paper/sim routes do not require acknowledgement.
