# moomoo quota contention awareness

**Date:** 2026-07-10
**Status:** Approved

## Problem

Earl runs eTape on two machines (Mac + Windows), each with its own local OpenD logged
into the same moomoo account. Dual OpenD login is confirmed to work. The account-level
quotas are shared across both: 100 subscription slots and 100 historical K-line slots
(base tier). The engine's existing slot budget (`quota_slots`, default 100, enforced in
`feed/opend/subman.go`) is per-instance and has no visibility into what the account is
actually using — so a second instance can silently drain the shared pool. The scanner
pool alone can hold 30 symbols × 2 subtypes = 60 slots; each focused US symbol costs 4.

Same stock+subtype subscribed from both machines costs 1 slot account-wide (quota is
released only when *all* connections unsubscribe), and historical quota is per unique
stock per 30 days — so overlapping watchlists are cheap; divergent ones collide.

## Decisions (from design discussion)

- **Scenario:** accidental overlap — Earl normally trades on one machine; the risk is
  forgetting the other is running. Detect + warn loudly.
- **Response:** warn only. No behavior change — subman budgeting/eviction untouched.
- **Surface:** one-shot toast on threshold crossings + persistent quota rows in the
  Connection Status panel.
- **Approach:** server-authoritative quota polling via `Qot_GetSubInfo` (3003) +
  the already-wired history-quota check (3104). No eTape-to-eTape coordination
  infrastructure — moomoo's server is the shared state.

## Architecture

```
OpenD ──(Qot_GetSubInfo 3003, all-conn; Qot_RequestHistoryKLQuota 3104)──▶
  quota poller (60s, engine) ──▶ state machine ──▶
    HealthSnapshot.Quota  (sys.health, persistent state)
    SysEvent{Level}       (sys.events, transitions only)
      └─▶ UI event→toast bridge (warn/danger → toast)
      └─▶ ConnectionStatusPanel quota rows
```

## Engine: detection

### New client method — `Qot_GetSubInfo` (3003)

The protobuf is already compiled (`engine/internal/feed/opend/pb/qotgetsubinfo/`);
the constant `ProtoQotGetSubInfo` exists (`protoid.go:14`). Add a request/response
method in the same style as `historyQuota()` (`backfill.go:252`). Send
`C2S{IsReqAllConn: true}`; consume `S2C.TotalUsedQuota`, `S2C.RemainQuota`, and
`S2C.ConnSubInfoList` (per-connection subscription detail; entries carry an
own-connection flag — exact field name confirmed during implementation).

### Quota poller

Ticks every 60s (code constant, well under moomoo request rate limits). Each tick:

1. `Qot_GetSubInfo(allConn)` → sub-quota totals + connection list.
2. `historyQuota()` (3104, existing) → hist used/remain.
3. Compute foreign usage and update the state machine.

**Foreign detection, two paths:**

- **Primary — connection list:** any connection in `ConnSubInfoList` that is not this
  instance's own and holds subscriptions ⇒ FOREIGN. This catches the identical-watchlist
  case, where account-wide dedupe makes the totals arithmetic blind
  (`total ≈ own` even with two instances running the same scanner pool).
- **Fallback — totals arithmetic:** `foreign = TotalUsedQuota − own connections' usage`.
  Works even if the all-conn list turns out to be local-OpenD-only, because the totals
  are server-side account numbers.

Which path is primary is settled by the empirical verification task (below).

### State machine

States: `OK → FOREIGN → LOW → EXHAUSTED` (severity-ordered; the highest applicable wins).

- **FOREIGN:** foreign usage detected. Requires 2 consecutive polls to enter
  (debounces moomoo's ≤1-minute quota-release lag after a connection closes).
- **LOW:** `RemainQuota < quota_warn_headroom`.
- **EXHAUSTED:** `RemainQuota == 0`.
- History quota has an independent OK/LOW check: `histRemain < hist_quota_warn_remain`.

**Transitions only** emit a `SysEvent` — a persistent condition never re-fires:

| Transition | Level | Example detail |
|---|---|---|
| → FOREIGN | info | `another OpenD client is using 15 subscription slots` |
| FOREIGN → OK | info | `other OpenD client released its subscriptions` |
| → LOW | warn | `8 subscription slots remaining account-wide` |
| → EXHAUSTED | danger | `subscription quota exhausted account-wide` |
| hist → LOW | warn | `7 historical K-line slots remaining (30-day window)` |

Rule: every state change emits one event at the level of the state being *entered*
(recovering to OK or downgrading to FOREIGN emits `info`). The table shows the wording
pattern, not an exhaustive list.

**Error handling:** a failed poll (OpenD down/timeout) skips the tick and holds the last
state — no event spam; the existing `engine-moomoo` health link already reports the feed
being down. On reconnect the next successful poll resumes normally.

## Engine → UI plumbing

No new WS topic. Two payload extensions (types regenerate into TS via tygo):

- **`HealthSnapshot`** (`sys.health`, `uihub/payloads.go`) gains an optional
  `Quota *QuotaInfo` field:
  `{ subUsed, subRemain, subOwn, subForeign, histUsed, histRemain, state }`.
  The health poller embeds the latest quota snapshot on each publish (quota poller and
  health poller share the snapshot; the quota poll keeps its own 60s cadence).
- **`SysEvent`** (`sys.events`) gains a `Level` field: `info | warn | danger`
  (absent/empty = info, so existing events are unaffected).

## UI

### Event→toast bridge (new, generic)

A small subscriber beside `HealthStore` watches incoming `sys.events` and pushes any
`warn`/`danger` event into the existing `useToasts()` system (`chrome/Toast.tsx`),
mapping `warn → warn`, `danger → danger`. Deduped by event kind+detail so each
transition toasts exactly once. This is the first engine-event→toast path; any future
engine alert with a level gets a toast for free.

### Connection Status panel

`ConnectionStatusPanel.tsx` gains a quota section under the link rows, driven by
`HealthSnapshot.Quota`:

```
Sub quota   62/100 used   this eTape 47 · others 15    ● amber
History     41/100 used                                 ● green
```

Dot color by state: green OK, amber FOREIGN or LOW, red EXHAUSTED. Section hidden until
the first quota snapshot arrives (field is optional).

## Config

In the existing `Feed` block (`engine/internal/config/config.go`, beside `quota_slots`):

| Key | Default | Meaning |
|---|---|---|
| `quota_warn_headroom` | 12 | warn when account-wide remaining sub slots drop below this (three focused US symbols' worth) |
| `hist_quota_warn_remain` | 10 | warn when 30-day historical K-line slots remaining drop below this |

Poll cadence is a code constant (60s) — not configurable until a reason exists.

## Edge cases

- **Identical watchlists:** handled by connection-list detection (primary path above).
- **Non-eTape consumers** (SDK skill scripts, prototypes) show as foreign; event wording
  says "another OpenD client", which is accurate.
- **eTape's own future moomoo broker-adapter connection** subscribes nothing; counted as
  own (or zero-sub foreign at worst) — no false positive either way.
- **Quota-release lag** (1-minute moomoo rule): 2-poll debounce on FOREIGN entry.
- **UI reload:** panel repopulates from the next `sys.health` publish; toast dedupe state
  is client-local, so a reload doesn't re-toast old transitions.
- Options quota fields (`OptionUsedQuota` etc.) are ignored — out of scope (US stocks only).

## Testing

- **Go:** state-machine unit tests (transition table, 2-poll debounce, event levels,
  poll-failure holds state); encode/decode test for the 3003 client method against
  canned protobuf bytes; `HealthSnapshot.Quota` marshal test.
- **UI:** toast-bridge test (warn event → one toast; repeat event deduped; info event →
  no toast); panel render test for the quota rows and state colors.

## Task 1: empirical verification (before implementation)

With the Windows OpenD holding subscriptions, run the moomooapi skill's
`query_subscription.py` (`is_all_conn=True`) against the Mac OpenD and confirm:

1. `TotalUsedQuota`/`RemainQuota` are account-global (reflect the Windows subs).
2. Whether `ConnSubInfoList` shows the remote OpenD's connections (decides the primary
   detection path).
3. Cross-machine dedupe: the same stock+subtype from both machines costs 1 slot.

Record results in the spec or a dated doc note; if (1) fails, the approach needs rework
and implementation must not proceed.

## Out of scope

- Any behavior change on contention (throttling, pool shrinking, budget coordination).
- eTape-to-eTape discovery/heartbeat.
- Options quota; non-US markets.
