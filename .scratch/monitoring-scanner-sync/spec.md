# Monitoring Workspace Scanner Sync

Status: ready-for-agent

## Problem Statement

eTape's Monitoring layout is currently a normal, replaceable preset with seeded symbols. A trader who wants to watch the highest-ranked Scanner symbols must manually load every chart, repeatedly decide which Scanner instance matters, and risk losing the relationship when opening or closing windows. The layout cannot act as a durable monitoring desk: its charts do not continuously follow Scanner membership, its chart count is not the Scanner target count, and ordinary preset/export behavior carries selected symbols that are not part of a portable layout.

The trader needs a dedicated **Monitoring Workspace** that stays available as a named workspace, can be arranged freely, and can continuously maintain its pinned Chart Panels from one explicitly chosen **Scanner Source**. Scanner rank changes must be useful without making the chart wall visually unstable: rank swaps must not reshuffle charts, while a symbol that leaves the relevant ranked set must be replaced. The behavior must survive restart, work across windows, pause honestly when it has no usable target, and never make a symbol-free layout silently reopen as AAPL.

## Solution

Create one reserved, permanent Monitoring Workspace. Its workspace identity cannot be renamed or deleted, but its Panel Groups, layout, Scanner Panels, Chart Panels, and Link Group choices remain editable. Opening Monitoring opens or reuses its dedicated window instead of replacing the current workspace with a preset.

The first Monitoring Workspace has four pinned, unassigned Chart Panels, a Scanner Panel, and an unassigned Stock Info Panel. A Scanner Panel header provides the explicit **Follow Monitoring** control. Selecting a Scanner Source enables persistent **Scanner Sync**; only one source is active at a time, and the source may be in any open or later-closed workspace window.

Scanner Sync maps ranked Scanner symbols to exactly the pinned Chart Panels in the Monitoring Workspace. It maintains stable chart membership and position: a rank swap alone does not move symbols between charts. It replaces only charts whose current symbol is no longer in the relevant ranked set, fills incoming symbols into eligible slots from left to right, coalesces changes to at most once per second, and preserves every chart setting other than its symbol. Linked Chart Panels opt out by design. Sync remains enabled but paused when it lacks a usable source or pinned Chart Panel.

All built-in layouts and future Layout downloads become symbol-free. They retain structural layout, non-symbol settings, Link Group membership, and relevant configuration, but not per-panel selections or Link Group focused symbols. User-created saved workspaces and already-downloaded files are not erased.

## User Stories

1. As an active trader, I want one dedicated Monitoring Workspace, so that I always have a stable place to watch Scanner candidates.
2. As an active trader, I want opening Monitoring to open or reuse its own window, so that it does not replace the workspace I am currently using.
3. As an active trader, I want the Monitoring Workspace to remain available after its window is closed, so that closing a window does not delete my monitoring arrangement.
4. As an active trader, I want the Monitoring Workspace protected from rename and delete actions, so that it remains a reliable permanent destination.
5. As an active trader, I want to edit Monitoring's Panel Groups and arrangement, so that the permanent workspace can fit my changing workflow.
6. As an active trader, I want the initial Monitoring Workspace to contain four Chart Panels, so that I have an immediately useful default wall.
7. As an active trader, I want to add or remove Chart Panels from Monitoring, so that the number of symbols followed can grow or shrink with my setup.
8. As an active trader, I want the default Monitoring Chart Panels to be pinned, so that they participate in Scanner Sync without needing a new link mechanism.
9. As an active trader, I want a linked Chart Panel to opt out of Scanner Sync, so that I can keep a manually coordinated chart beside the monitoring wall.
10. As an active trader, I want the number of Scanner Sync targets to equal the number of pinned Chart Panels, so that no separate chart-count setting can drift from my layout.
11. As an active trader, I want to remove every pinned Chart Panel without being blocked, so that I can reshape Monitoring freely.
12. As an active trader, I want Scanner Sync to remain on but paused when no pinned Chart Panel exists, so that it resumes when I add or re-pin a chart.
13. As an active trader, I want new pinned Chart Panels to receive the highest missing ranked symbols on the next sync, so that expanding the wall needs no manual reassignment.
14. As an active trader, I want making a chart linked or removing it to reduce the target count without reshuffling the remaining charts, so that layout edits preserve visual continuity.
15. As an active trader, I want to choose the Scanner Source from a Scanner Panel header, so that I can explicitly see which Scanner controls Monitoring.
16. As an active trader, I want any Scanner Panel to be eligible as the Scanner Source, so that I can follow a Scanner from Monitoring or another workspace window.
17. As an active trader, I want only one active Scanner Source, so that the chart wall never receives competing rank instructions.
18. As an active trader, I want choosing a Scanner Source to turn Scanner Sync on, so that source selection is one deliberate action.
19. As an active trader, I want the active Scanner header control to turn Sync off while remembering its source, so that I can pause and later resume the same relationship.
20. As an active trader, I want Scanner Sync and its source selection persisted across restart, so that an enabled Monitoring Workspace continues following without setup repetition.
21. As an active trader, I want closing the Scanner Source's host window not to stop an enabled sync, so that my Monitoring Workspace can continue following the chosen Scanner configuration.
22. As an active trader, I want Sync to pause when the chosen Scanner Panel is deleted, so that eTape never silently switches to a different Scanner.
23. As an active trader, I want to select a replacement Scanner Source explicitly after deletion, so that source changes are intentional.
24. As an active trader, I want the source's displayed sort order to determine rank order, so that the Monitoring wall follows what I see in that Scanner.
25. As an active trader, I want source sorting to remain independent from other Scanner Panels' sorts, so that choosing a source is meaningful even though Scanner results share the current engine filter.
26. As an active trader, I want Scanner Sync to react continuously to refreshed rankings, so that the wall remains relevant during a moving market.
27. As an active trader, I want frequent Scanner changes coalesced to at most one chart update per second, so that the wall remains readable and does not churn.
28. As an active trader, I want the top ranked symbol initially assigned to the upper-left chart and later symbols assigned in chart-slot order, so that the initial wall reads naturally.
29. As an active trader, I want each chart's position to remain fixed through rank swaps, so that a symbol does not jump around merely because two ranks trade places.
30. As an active trader, I want a chart symbol replaced only when it falls out of the relevant Scanner membership, so that meaningful chart context is not needlessly reset.
31. As an active trader, I want a newly ranked missing symbol placed into the earliest eligible chart slot, so that replacements are deterministic.
32. As an active trader, I want fewer Scanner rows than pinned Chart Panels to leave unmatched current chart symbols in place, so that temporary Scanner scarcity does not blank or thrash the wall.
33. As an active trader, I want a status such as Following 2/4 when only two ranked symbols are available for four targets, so that I can understand incomplete coverage.
34. As an active trader, I want a manually typed symbol on a participating pinned Chart Panel restored by the next Scanner Sync, so that the Sync contract is predictable.
35. As an active trader, I want to make a chart linked or turn Sync off when I need a manual symbol to persist, so that there is a clear opt-out path.
36. As an active trader, I want a Scanner-driven symbol change to preserve the chart's timeframe, indicators, drawings, settings, dimensions, and placement, so that only the watched instrument changes.
37. As an active trader, I want unassigned Monitoring charts to say Waiting for Scanner Sync, so that a blank chart communicates why it has no symbol.
38. As an active trader, I want unassigned symbol-bearing panels outside Monitoring to prompt me to type or link a symbol, so that blank layouts remain usable without a default ticker.
39. As an active trader, I want the Monitoring Stock Info Panel to begin with no Link Group, so that it does not appear to follow a hard-coded blue symbol.
40. As an active trader, I want Stock Info to follow whichever Link Group I select, so that it continues to use the normal Link Group behavior.
41. As an active trader, I want all built-in workspace presets to omit default selected symbols, so that a new layout does not silently start on a particular ticker.
42. As an active trader, I want Layout downloads to omit every per-panel selected symbol, so that the downloaded file describes layout rather than my current watchlist.
43. As an active trader, I want Layout downloads to omit Link Group focused symbols too, so that linked panels cannot restore a selection indirectly.
44. As an active trader, I want Layout downloads to keep panel arrangement, Link Group membership, venue linkage, timeframes, indicators, and other non-symbol settings, so that the export remains useful.
45. As an active trader, I want a Layout download to retain an enabled Monitoring intent but omit a cross-window Scanner Source reference, so that the file is portable.
46. As an active trader, I want an imported enabled Monitoring layout with no source to show as paused until I choose one, so that it never follows an accidental Scanner.
47. As an active trader, I want an unassigned Order Ticket unable to place an order, so that removing default symbols cannot create an accidental trade target.
48. As an active trader, I want place-order hotkeys to remain blocked without a symbol, so that keyboard order entry has the same safety boundary.
49. As an active trader, I want my existing saved workspace selections left intact, so that adopting this feature does not erase personal layouts.
50. As an active trader, I want older downloaded files left untouched on disk, so that this feature does not perform destructive file cleanup.
51. As a maintainer, I want the Scanner Source represented by stable workspace and panel identities rather than display names, so that rename and title changes cannot retarget Sync.
52. As a maintainer, I want the stable-membership decision testable without rendering Dockview or consuming live market data, so that ranking edge cases stay deterministic.
53. As a maintainer, I want Scanner data to continue flowing through the existing imperative store, so that high-frequency data does not enter React state.
54. As a maintainer, I want unassigned panels to make no unnecessary symbol demand, so that symbol-free layouts do not consume data quota for a hidden fallback.
55. As a maintainer, I want no new engine or WebSocket contract for this feature, so that the established Scanner feed remains the sole ranked-data contract.
56. As a maintainer, I want the generated TypeScript WebSocket contract untouched, so that client/server ownership remains unchanged.

## Implementation Decisions

- Use the domain terms **Monitoring Workspace**, **Scanner Sync**, **Scanner Source**, **Unassigned Chart Panel**, **Link Group**, and **Stock Info Panel** as defined in the repository glossary.
- Reserve one workspace identity for Monitoring. It is a workspace-level policy, not a normal preset application: the catalog opens or reuses it, and ordinary rename/delete controls must reject it. Its saved Panel Groups, panel instances, and layout remain editable.
- Seed the Monitoring Workspace only when it has no saved usable layout. The seed contains four pinned, unassigned Chart Panels, one Scanner Panel, and one unassigned Stock Info Panel. Reopening Monitoring never reapplies the seed over user edits.
- Treat a legacy or pre-existing saved workspace under the reserved identity as user data. Do not overwrite it merely because the identity is now reserved.
- Keep Trading as a normal reusable preset, but remove its seeded symbols. Preserve its non-symbol structural relationships, including Link Group membership and panel settings such as chart timeframe and indicators.
- Define a Scanner Sync target as a Chart Panel in the Monitoring Workspace whose Link Group is null. Other pinned symbol-bearing panels are not Scanner Sync targets.
- Persist Scanner Sync only with the Monitoring Workspace. Its persistent state includes whether the user intends Sync to be enabled and, when selected, a Scanner Source identity made of workspace identity plus Scanner Panel identity.
- Persist source behavior by identity rather than title or window title. The source's stored Scanner Panel settings supply its configured visible sorting behavior after its host window closes.
- A Scanner header is the only source-selection and Sync-toggle surface. Selecting an inactive Scanner makes it the Scanner Source and enables Sync. Toggling the active source off preserves the remembered source for later resume. There is no duplicate top-bar control.
- Allow a Scanner Source from any workspace. Closing its host window must not clear the source or stop Sync. Deleting that Scanner Panel makes Sync paused; it must not fall back to another Scanner automatically.
- Reuse the current global Scanner results and the selected source's local visible sort. Do not introduce independent per-Scanner engine filters in this iteration.
- Add one small pure Scanner Sync planner as the behavior seam. Its inputs are the Monitoring Workspace's ordered chart slots, the current source-ranked rows, and current Sync availability; its outputs are the exact symbol patches and user-facing follow/paused status. The planner has no UI, storage, timer, or market-data side effects.
- Keep orchestration in the existing workspace shell. It observes the existing Scanner store, loads persisted source settings as needed, coalesces source changes to no more than one planning application per second, applies planner patches through the existing workspace update route, and persists the resulting workspace state.
- Give each participating Chart Panel a persistent chart-slot identity and deterministic order initialized from the Monitoring wall's upper-left-to-lower-right order. Moving a panel later moves its existing chart context; a rank swap must not reassign symbols between slot identities.
- For a full ranked set, retain every chart slot whose current symbol remains in the top set. Assign each top-ranked symbol not already retained to the earliest remaining slot, ordered by scanner rank. Do not reorder retained slots.
- When the source supplies fewer rows than target slots, preserve all unmatched existing symbols after the available ranked symbols have been assigned. Do not clear a chart merely because there are not enough scanner rows. Report the available count over target count.
- When a pinned target is added, linked, unlinked, or removed, recalculate only on the next coalesced plan. Preserve surviving slot symbols whenever they still belong to the relevant ranked set.
- When no pinned Chart Panel exists, leave enabled Sync in the paused state. When a source is unavailable, deleted, or has no usable current rows, also show a paused or incomplete-following state rather than silently selecting a replacement source.
- A manual symbol entry on a participating pinned Chart Panel remains an ordinary panel edit, but the next successful Scanner plan may restore the ranked symbol. Link Group assignment and Sync-off are the deliberate opt-out mechanisms.
- Scanner-driven chart changes may patch only the panel symbol. They must preserve timeframe, indicators, drawings, chart appearance settings, panel geometry, panel grouping, and position.
- Make unassigned state first-class instead of falling back to AAPL. Chart, DOM Ladder, Time & Sales, Order Ticket, Stock Info, and Locates must tolerate an absent effective symbol without issuing a demand or rendering a fake selected instrument.
- Show Waiting for Scanner Sync specifically for unassigned Monitoring Chart Panels. Other unassigned symbol-bearing panels use the existing type-to-load/linking affordance and a neutral no-symbol state.
- Keep Order Ticket submission and place-order template paths blocked when no effective symbol exists. Preserve safety actions whose existing semantics intentionally do not require a focused symbol.
- Do not change Stock Info's normal Link Group semantics. Remove the Monitoring seed's blue assignment so Stock Info begins unassigned; a user-selected Link Group remains the only way it follows a focused symbol.
- Make future Layout downloads structural. Before serialization, copy the workspace and remove every panel-level symbol setting and every persisted Link Group focused symbol. Retain panel layout, panel IDs, Link Group membership, focused venues, non-symbol panel settings, and layout version.
- For a Monitoring Layout download, retain only Sync's enabled intent. Omit the Scanner Source identity because it can point to a different workspace window and is not portable. Imported enabled Sync without a source is paused until the user chooses one.
- The Layout export rule applies to the General settings Layout download only. The independent Orders and hotkeys export remains outside this feature's symbol-layout policy.
- Preserve compatibility and data safety: do not delete or rewrite existing workspace configurations or files already downloaded by the user. Newly produced Layout downloads are the structural form.
- Keep the existing Scanner data and demand architecture. No Go market-data behavior, scanner wire message, generated contract, broker adapter, or execution API is added or changed for Scanner Sync.
- Update user-facing and repository documentation that describes workspace behavior, Layout export/import behavior, Monitoring, Scanner interaction, or symbol demand after implementation.

## Testing Decisions

- A good test asserts a trader-visible result or a published workspace state: chosen source, enabled or paused state, chart symbols, retained chart settings, source indicator, downloaded JSON content, and blocked order placement. It must not assert private timers, Dockview internals, React state structure, or planner implementation details.
- Use the pure Scanner Sync planner as the highest new behavior seam for ranking logic. Feed ordered chart slots and ranked rows, then assert exact symbol assignments and statuses.
- Through the planner seam, cover initial four-chart assignment, rank swaps with no repositioning, one new entrant replacing one departed symbol, multiple entrants, manual chart symbols restored on the next plan, chart-slot ordering, and preservation of retained symbols.
- Through the planner seam, cover fewer rows than targets, zero rows, no pinned targets, added pinned targets, linked opt-outs, removed targets, duplicate or unusable row handling if the existing Scanner view can surface it, and deterministic earliest-slot fill behavior.
- Through existing workspace-shell integration tests, verify first-open seeding, open-or-reuse behavior, reserved rename/delete rejection, persistence and restart hydration, source host-window closure, source panel deletion, explicit replacement-source selection, and no mutation of unrelated workspaces.
- Through existing Scanner Panel interaction tests, verify the header control selects the source, displays active versus inactive state, toggles off while remembering its source, and remains understandable when Sync is paused.
- Through existing Scanner store and panel configuration test styles, verify that source ranking follows the selected Scanner's persisted visible sort while no new independent engine filter behavior is introduced.
- Through existing PanelFrame and symbol-bearing panel tests, verify a genuinely unassigned state for Chart, DOM Ladder, Time & Sales, Order Ticket, Stock Info, and Locates. Assert no fallback ticker appears and no symbol demand is made until a symbol is supplied.
- Extend chart tests to prove the Monitoring-specific waiting message and that a Scanner-driven symbol update preserves timeframe, indicators, drawings, and other chart settings.
- Extend Order Ticket and order-template tests to prove direct placement and place-order hotkeys cannot submit when no effective symbol exists. Preserve the existing tests for intentionally symbol-independent safety actions.
- Through existing Layout export tests, verify that a layout download removes every panel symbol and Link Group focused symbol while retaining arrangement, panel settings unrelated to symbols, Link Group membership, focused venues, and normal export envelope fields.
- Add export coverage for Monitoring: enabled intent is retained, Scanner Source identity is omitted, and an imported layout with enabled intent but no source is visibly paused.
- Use workspace import/export tests to prove the new export operation does not mutate the live saved workspace, and that compatibility paths do not delete existing user choices.
- Reuse the repository's existing Scanner, workspace shell, backup, PanelFrame, Chart Panel, Stock Info, Time & Sales, Ladder, Order Ticket, and hotkey test conventions rather than adding a new end-to-end harness solely for this feature.
- Run the relevant UI test suite and type check for every ticket. Before handoff, run the repository's CI-equivalent Windows checklist because this feature spans workspace persistence, UI behavior, and execution safety.

## Out of Scope

- Independent per-Scanner engine filters, sessions, or result universes.
- Multiple simultaneous Scanner Sources or blending symbols from multiple Scanners.
- Automatic source selection, automatic source replacement, or source selection by Scanner title.
- Re-ranking or reshuffling chart positions solely because rank order changes.
- A user-configurable replacement policy, reshuffle mode, custom target-count setting, or additional Sync modes.
- Making Link Groups act as Scanner Sync slots or adding more Link Group colors.
- Syncing DOM Ladder, Time & Sales, Order Ticket, Stock Info, Locates, Watchlist, or any non-Chart Panel from Scanner rank.
- Automatically assigning a Link Group to Stock Info or any other panel.
- Deleting current user workspace symbols, migrating saved personal workspaces to blank, or removing historical downloaded files.
- Changing the independent Orders and hotkeys export.
- Redesigning Scanner filters, Scanner ranking algorithms, market-data subscriptions, broker adapters, order execution contracts, or generated WebSocket types.
- A new visual dashboard, alerting, sound, automated order behavior, or trading rule based on Scanner rank.
- Persisting a portable cross-window Scanner Source in a Layout download.

## Further Notes

- The Monitoring Workspace is a reserved workspace, not a normal replaceable preset. Its permanence protects the workspace identity; it does not freeze the trader's layout.
- A Link Group is a global cross-window focus mechanism and remains distinct from Scanner Sync. A grouped chart follows its Link Group by normal behavior and is intentionally excluded from Scanner Sync.
- The chosen Scanner Source uses the existing global Scanner result set plus its own stored local sort. This supports cross-window following without creating a second scanner data pipeline.
- When a live Link Group has a focused symbol from another window, a grouped panel may follow it by established Link Group behavior. The new symbol-free seed and export policy merely ensure that the layout itself does not introduce that selection.
- No prototype is required: the state policy, visual behavior, persistence rules, and test seam were settled through the grilling session and codebase investigation.
- The repository glossary in CONTEXT.md is the source of truth for the feature's domain language and must stay aligned with this specification.
