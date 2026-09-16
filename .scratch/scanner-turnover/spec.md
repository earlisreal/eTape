# Scanner Dollar Turnover

Status: Implemented on 2026-09-16.

Implementation plan: [Scanner Dollar Turnover](../../docs/plans/2026-09-16-scanner-dollar-turnover.md).

## Settled decisions

- Turnover means Dollar Turnover: total US-dollar value traded.
- Add a sortable Scanner column and an optional minimum filter.
- Measure the current session, matching Scanner Volume; reset the metric at session transitions.
- Minimum turnover controls new board admission only. Existing rows remain sticky; changing the threshold rebuilds the board.
- Unavailable values display as an em dash, sort last in both directions, and fail an enabled minimum filter. Valid zero displays as `$0`.
- Sort/filter existing Scanner candidates; no new market-wide turnover discovery mode. Scanner Sync follows turnover sorting.
- Label the column `Turnover`, immediately after `Vol`; format compact dollar values and use the tooltip `Dollar value traded in the current session.` Sort/filter unrounded values.
- Input is `Min turnover ($M)`, allows decimals, and defaults to zero (disabled). Persist with shared Scanner filters; old settings default to disabled. Sort remains per panel.
- Use provider-reported turnover. Preserve a last valid value through a failed refresh only within the same session instance; clear it across session changes until matching data arrives.
- Missing, negative, or non-finite source values are unavailable; never estimate using last price times volume.

## Current-code evidence

- `engine/internal/scan/scan.go` discards turnover from session rank responses and batched snapshots; those protocol types already carry dollar turnover.
- RTH most-active discovery uses stock filtering; batched snapshots also refresh those rows.
- Scanner volume is session-specific. The sticky board spans a trading cycle and resets when post-market begins.
- Engine filters control admission; already admitted rows are retained. Changing filters clears and rebuilds the board.
- `engine/internal/uihub/wsmsg/payloads.go` owns the wire contract; TypeScript is generated.
- `ui/src/chrome/scannerSync.ts` shares sorting with Scanner Sync.

## Open decisions

None. Review the complete plan to confirm shared understanding before implementation.

## Comments

- 2026-09-16: Earl selected dollar value traded and accepted a sortable column plus optional minimum filter. Session and behavior choices remain open.
- 2026-09-16: Earl accepted current-session turnover, admission-only filtering, explicit unavailable values, and existing-candidate scope including Scanner Sync sorting.
- 2026-09-16: Earl accepted display, threshold units/persistence, and session-safe provider-value fallback recommendations.
