# Plan: Lazy Load on Pan Release + Per-Symbol Bar Cache + Dinosaur Icon

## Overview

Remove the prefetch-on-proximity trigger (`SCREENS_THRESHOLD`) from `OlderHistoryController`.
Trigger `LoadOlderBars` only after the user **releases** a pan or zoom gesture, so empty bars
are visible during a drag but no network round-trip fires until the gesture ends.
The per-symbol bar cache already works (BarStore never evicts). Add a 🦕 icon at the left
edge of the chart when the engine confirms no older bars remain.

## Change summary

| File | Change |
|---|---|
| `ui/src/render/chart/olderHistory.ts` | Remove prefetch math; rename to `triggerNow`; add `isExhausted()` |
| `ui/src/chrome/panels/ChartPanel.tsx` | Move trigger to `pointerup`/`keyup`/wheel-debounce; add dino icon state |
| `ui/src/render/chart/olderHistory.test.ts` | Rename call sites; add `isExhausted` test; drop proximity tests |
| `ui/src/chrome/panels/ChartPanel.test.tsx` | Update trigger mechanism in existing LoadOlderBars test |
| `ui/src/data/BarStore.ts` | No change — already the per-symbol cache |
| `engine/` | Verify `exhausted` field exists in `LoadOlderBars` ack; fix only if missing |

---

## Step 1 — `olderHistory.ts`: drop prefetch, rename to `triggerNow`, expose `isExhausted`

**File:** `ui/src/render/chart/olderHistory.ts`

### What to remove

- `SCREENS_THRESHOLD = 1.5` constant
- The entire proximity check block inside `maybeTrigger`:
  ```ts
  const screens = logicalRange.to - logicalRange.from;
  if (screens <= 0) return;
  const remaining = logicalRange.from - LEFT_PAD_BARS;
  if (remaining >= SCREENS_THRESHOLD * screens) return;
  ```
- The `logicalRange` parameter entirely — caller no longer passes bar-index range.
- The `LEFT_PAD_BARS` import (no longer needed once proximity math is gone).

### What to add / rename

Rename `maybeTrigger(logicalRange, isIntraday, requiredRangeMs?)` →
`triggerNow(isIntraday, requiredRangeMs?)`:

```ts
/**
 * Called once after the user releases a pan/zoom gesture (pointerup, keyup, wheel idle).
 * Fires LoadOlderBars if guards pass. Safe to call speculatively — inflight/exhausted/
 * cooldown/dedup guards prevent duplicate requests.
 */
triggerNow(
  isIntraday: boolean,
  requiredRangeMs: { from: number; to: number } | null = null,
): void {
  const kind: HistoryKind = isIntraday ? "intraday" : "daily";
  if (this.inflight[kind] || this.exhausted[kind]) return;
  if (this.deps.now() < this.cooldownUntil[kind]) return;
  if (!requiredRangeMs) return;

  const requiredStartMs = requiredRangeMs.from;
  if (this.lastAcceptedStartMs[kind] === requiredStartMs) return;

  this.inflight[kind] = true;
  this.clearTimer(kind);
  this.timers[kind] = setTimeout(() => {
    this.inflight[kind] = false;
    this.timers[kind] = undefined;
  }, TIMEOUT_MS);

  const daily = kind === "daily";
  this.deps
    .load(daily, requiredStartMs, requiredRangeMs.to)
    .then((ack) => this.settle(kind, ack, requiredStartMs))
    .catch(() => this.settle(kind, { status: "blocked" }, requiredStartMs));
}
```

Add `isExhausted`:
```ts
isExhausted(kind: HistoryKind): boolean {
  return this.exhausted[kind];
}
```

Keep unchanged: `reset()`, `settle()`, `clearTimer()`, `COOLDOWN_MS`, `TIMEOUT_MS`.

### Updated docstring

Replace the class docstring to say: fires only when called explicitly after gesture release,
not on every range-change event. All existing guards (inflight, exhausted, cooldown, dedup)
remain active.

---

## Step 2 — `ChartPanel.tsx`: gesture-release trigger + dino icon

**File:** `ui/src/chrome/panels/ChartPanel.tsx`

### 2a — Remove range-change trigger

Inside `clampRight` (the `subscribeVisibleLogicalRangeChange` handler), delete:
```ts
// DELETE this entire block:
if (range) {
  const vr = (facade as any).getVisibleRange();
  olderHistory.maybeTrigger(
    { from: range.from, to: range.to },
    isIntradayTimeframe(tfRef.current as Timeframe),
    vr ? { from: vr.from * 1000, to: vr.to * 1000 } : null,
  );
} else {
  olderHistory.maybeTrigger(null, isIntradayTimeframe(tfRef.current as Timeframe));
}
```

Keep the rest of `clampRight` exactly as-is (clamping + `scheduleRefreshSelection`).

### 2b — Add `triggerOlderHistory` helper

Add inside the mount effect, after `olderHistory` is constructed:
```ts
const triggerOlderHistory = () => {
  const vr = (facade as any).getVisibleRange() as { from: number; to: number } | null;
  olderHistory.triggerNow(
    isIntradayTimeframe(tfRef.current as Timeframe),
    vr ? { from: vr.from * 1000, to: vr.to * 1000 } : null,
  );
  // Update dino icon: both kinds must be exhausted.
  const done = olderHistory.isExhausted("intraday") && olderHistory.isExhausted("daily");
  setAllExhausted(done);
};
```

### 2c — Wire release events on `host`

Add event listeners on `host` (the chart container element) inside the mount effect.
All three paths call `triggerOlderHistory()`.

```ts
// Mouse/touch drag release
host.addEventListener("pointerup", triggerOlderHistory);

// Keyboard pan (ArrowLeft, ArrowRight, Home end the pan on keyup)
const onKeyUp = (e: KeyboardEvent) => {
  if (e.key === "ArrowLeft" || e.key === "ArrowRight" || e.key === "Home") {
    triggerOlderHistory();
  }
};
host.addEventListener("keyup", onKeyUp);

// Wheel zoom: debounce 200ms — no "wheel end" event exists
let wheelTimer: ReturnType<typeof setTimeout> | undefined;
const onWheel = () => {
  clearTimeout(wheelTimer);
  wheelTimer = setTimeout(triggerOlderHistory, 200);
};
host.addEventListener("wheel", onWheel, { passive: true });
```

Add to cleanup return:
```ts
host.removeEventListener("pointerup", triggerOlderHistory);
host.removeEventListener("keyup", onKeyUp);
host.removeEventListener("wheel", onWheel);
clearTimeout(wheelTimer);
```

### 2d — Dino icon state

Add near the top of `ChartPanel` component (with other `useState` declarations):
```ts
const [allExhausted, setAllExhausted] = useState(false);
```

Reset it when the symbol changes — inside `applySymbol` (already in the mount effect),
after `olderHistory.reset()`:
```ts
setAllExhausted(false);
```

### 2e — Dino icon JSX

Add inside the chart host container (the same `div` that holds `BarCloseTimer` and other
absolutely-positioned overlays), gated on `allExhausted`:

```tsx
{allExhausted && (
  <div
    style={{
      position: "absolute",
      left: 4,
      top: "50%",
      transform: "translateY(-50%)",
      pointerEvents: "none",
      opacity: 0.55,
      fontSize: 22,
      userSelect: "none",
    }}
    title="No more historical bars available"
  >
    🦕
  </div>
)}
```

`pointerEvents: none` is required — must not block chart pan/zoom interaction.

---

## Step 3 — `BarStore.ts`: verify, no code change

**File:** `ui/src/data/BarStore.ts` — **read-only**

`BarStore.series_` is a `Map<string, Bar[]>` keyed `${symbol}:${timeframe}`. Entries are
never evicted. `prependBatch` (called by the engine's `LoadOlderBars` response) prepends
older bars into the existing array for that key. Switching to a different symbol and back
re-reads the same map entry — all previously loaded bars are still there, no backfill needed.

**Action:** Read `BarStore.prependBatch` to confirm it writes to the correct key and does not
reset the array. If confirmed, zero changes needed.

> ponytail: no eviction strategy needed until memory becomes a measured problem.

---

## Step 4 — Engine: verify `exhausted` in `LoadOlderBars` ack

**Files:** `engine/internal/uihub/` (wherever `LoadOlderBars` command is handled)

Find the Go handler for `LoadOlderBars`. Confirm the JSON response includes:
```json
{ "status": "accepted", "value": { "exhausted": true } }
```
when no older bars exist (or when the configurable history limit is reached).

The UI `settle()` already reads `ack.value.exhausted` — this step only verifies the engine
is sending it. If the field is present and correct: **no engine changes needed**. If missing:
add `Exhausted bool \`json:"exhausted"\`` to the response struct and set it to `true` when
the archive query returns zero new bars.

---

## Step 5 — `olderHistory.test.ts`: update for new API

**File:** `ui/src/render/chart/olderHistory.test.ts`

1. Replace all `c.maybeTrigger(range(from, to), isIntraday, requiredMs?)` calls with
   `c.triggerNow(isIntraday, { from: fromMs, to: toMs })`.
   - The old logical-range argument is gone entirely.
   - `requiredRangeMs` is now UTC ms directly (was already passed as the third arg in most tests).

2. **Remove** tests that specifically assert the `SCREENS_THRESHOLD` proximity threshold
   (e.g., "does not fire when enough bars remain left of viewport") — that behavior is deleted.

3. **Add** tests:
   - `triggerNow` fires when guards clear.
   - `triggerNow` is a no-op when `requiredRangeMs` is null.
   - `isExhausted("intraday")` returns `false` before any ack.
   - `isExhausted("intraday")` returns `false` after a `blocked` ack.
   - `isExhausted("intraday")` returns `true` after an `accepted` ack with `value.exhausted: true`.
   - `isExhausted("intraday")` returns `false` after `reset()`.

4. Keep all existing guard tests (inflight, cooldown, timeout, dedup) — just rename the call.

---

## Step 6 — `ChartPanel.test.tsx`: update trigger test

**File:** `ui/src/chrome/panels/ChartPanel.test.tsx`

The existing test at line 167 ("passes demanded UTC-ms range in LoadOlderBars") currently
triggers by firing a `visibleLogicalRangeChange` event. Update it:

1. Instead of firing a range-change, fire a `pointerup` event on the chart host element:
   ```ts
   host.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
   ```
   Ensure `facade.getVisibleRange()` returns a mock range before the event fires so
   `triggerOlderHistory` has a non-null range to pass.

2. Keep the assertion unchanged:
   ```ts
   expect(commands.sendCommand).toHaveBeenCalledWith("LoadOlderBars", {
     symbol: ..., daily: ..., requiredStartMs: ..., requiredEndMs: ...,
   });
   ```

3. **Add** test: firing `pointerup` after engine returns `exhausted: true` (for both kinds)
   causes `allExhausted` state to become `true` (dino icon renders).

4. **Add** test: symbol change (applySymbol) resets `allExhausted` to `false`.

---

## Dependency order

```
Step 4 (verify engine — read-only, no blocking dep)
Step 3 (verify BarStore — read-only, no blocking dep)
Step 1 → Step 5 (test matches new API; do together)
Step 2 → Step 6 (panel test matches new behavior; do together)
```

Steps 1+5 can be one commit. Steps 2+6 can be one commit.

---

## Constraints for LLM execution

- **Do not touch** `applyBars`, `applyIndicators`, `refreshBarCaches`, `setAllBars` in
  `ChartController.ts`. Indicator and bar data flow is engine-computed; UI only displays it.
- **Do not add** a new bar-cache layer. `BarStore` is already the cache.
- **Keep all existing guards** in `OlderHistoryController`: inflight, exhausted, cooldown,
  timeout, `lastAcceptedStartMs` dedup. They remain valid with the new trigger model.
- **`pointerEvents: none`** on the dino icon is mandatory — must not intercept chart events.
- **Wheel debounce timer** must be cancelled in the mount-effect cleanup to prevent state
  updates after unmount.
- `allExhausted` checks **both** `"intraday"` and `"daily"` — the dino only appears when the
  engine has confirmed no more bars of either kind remain.
- The configurable history limit on the engine side (however it's configured) already gates
  the `exhausted` signal — no UI change needed to respect it.
