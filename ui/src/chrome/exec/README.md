# Execution UI

Order ticket, hotkeys, sizing, venue selection, and arm/disarm presentation. Inputs: user intent plus account/position stores; outputs: typed wire commands. UI never bypasses engine gates. Test: `npm test -- exec`.

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
validation, venue fallback, engine risk gates, sounds, and acknowledgements are unchanged.
