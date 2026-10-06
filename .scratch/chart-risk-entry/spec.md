# Chart Risk Entry

Status: approved — Earl confirmed the design on 2026-10-06 and requested implementation.

## Behavior

An Action Template hotkey starts a one-shot two-point order tool on the active grouped Chart Panel in the focused window. First point is a BUY STOP_LIMIT trigger; second is the linked SELL STOP_LIMIT trigger. Support drag/release and click/click, dynamic imperative preview, editable endpoints, Enter to submit, and a global default-off auto-send-on-completion setting. Escape/right-click/focus loss/context changes cancel. Require confirmed flat position and no other working orders in the venue+symbol.

Presets save fixed dollar risk, Risk Cash %, or Risk BP %, positive risk value, separate dollar/percent buy and sell Limit Cushions, and hotkey. Cushions default to zero. Cash % and BP % mean loss budget, not purchase value. Compute whole shares as floor(budget/(buyLimit-sellLimit)) after cushions/tick rounding, constrained by cash for Risk Cash %, buying power otherwise. Never silently increase approved preview quantity. Invalid prices, unavailable account/position/market data, and existing engine gates block submission. Already-reached buy triggers are allowed with WILL TRIGGER NOW. Estimate excludes fees and does not guarantee fills/loss cap.

Both conditionals use engine custody in PRE/RTH/POST, retain selected venue/account acknowledgement, and expire at scheduled DataClose, including early close. Preserve pair across session boundaries. No overnight, take-profit, shorts, scale-in, auto-flatten, or unrelated-position protection. Partial entry fills grow protection. Stop activation cancels remaining buy; protect late buy fills without duplicate/oversized exits. Cancel/reject entry preserves filled protection; Cancel Protection cancels remaining entry and stop but leaves acquired shares open. Kill/cancel-all remain cancellation actions. Disarm/day-loss stop buying without disabling healthy linked exits. Restart/feed/broker loss require reconciliation/manual Resume; persist link/child identity before venue requests and never blindly replay uncertain submissions. UI closure leaves engine custody running. Expiry/protection failure visibly warn about remaining shares.

Dragging submitted markers applies on release, independently of initial auto-send setting. Before buy activation, recompute quantity against original dollar risk budget and saved cushions. Lock buy quantity at activation. After activation, permit price-only changes with before/after estimated risk and above-budget warning; protective stops may rise above entry after fills. Trigger drag preserves configured cushion; working child drag changes limit only. Revalidate at release and reject changes whose meaning changed during the drag. Pending/UNKNOWN/rejected venue amendments remain distinct from confirmed prices. Existing cancel/resubmit adapters may interrupt protection; failed/ambiguous replacement must remain visible and must not blindly retry.

## Confirmed test boundaries

- Engine commands, broker events, and recovery.
- Chart pointer/hotkey interactions and wire commands.
- Settings preset persistence.
- Review baseline: 8a763dfc9b59d34d6f532e81ad1f93afbcd1d3ce.

## Comments

All 23 grilling decisions accepted; chart dragging explicitly replaces the initial read-only submitted-price proposal. Implementation authorized by the implement skill invocation.
