# Scanner Float Fallback (3203 on-demand snapshots) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the scanner's permanently-broken 3215 float-universe warm-up with an on-demand `Qot_GetSecuritySnapshot` (3203) cache that resolves float for exactly the symbols on the rank board, caches per ET day, and drives three-state float-filter semantics.

**Architecture:** The `scan.Poller` drops the `universe` map / `refreshUniverse` / `filterpb` (3215) path entirely. Each poll, after fetching the 3410 rank, it snapshots the board symbols not yet cached in one 3203 batch (chunked at 400 codes, capped at 8 requests/poll, binary split-retry to isolate the one-bad-code-fails-the-batch case), populating a `floats map[string]floatEntry` cache before the pure `rankRows` transform. The cache clears on the ET-day boundary alongside the seen-sets.

**Tech Stack:** Go, `google.golang.org/protobuf`, moomoo OpenD raw-TCP/protobuf client (`internal/feed/opend`), `log/slog`, `internal/clock` (Fake clock for tests).

## Global Constraints

- **US stocks only** — `symbolOf` always prefixes `US.`; snapshot requests use market `QotMarket_QotMarket_US_Security` (enum value `11`). (CLAUDE.md scope decision.)
- **No new config** — the 8-request cap and 400-code chunk are `const`, not config fields. Reference: spec §Config.
- **proto2 required fields are enforced on `proto.Unmarshal`** — any test protobuf response that is *successfully decoded* by production code must set every `required` field, or the client-side `proto.Unmarshal` fails (see `kl()` in `engine/internal/feed/opend/backfill_test.go`). Error responses (`retType != 0`) need only `RetType` (+ optional `RetMsg`).
- **`slog` for logging**, matching engine convention (e.g. `internal/broker/tradezero/ws.go`): `slog.Warn("scan: ...", "err", err)`.
- **Outer `Request{C2S: ...}` wrapper** on every OpenD call — a bare `C2S` serializes differently and OpenD rejects it (see `fetchRank` in `scan.go` and every call site in `feed/opend/backfill.go`).
- Work happens on local `main` (or a worktree off it). **Before any `git checkout` in the shared repo root, confirm no concurrent session holds it on another branch.**

---

## File Structure

- **Modify:** `engine/internal/scan/scan.go` — the whole change lives here. Remove `refreshUniverse` + 3215 path; add `floatEntry`, the `floats` cache, `resolveFloats`/`snapshotBatch` (3203), `codeOf`; rewrite `rankRows` (three-state) and `pollOnce`.
- **Modify:** `engine/internal/scan/scan_test.go` — adapt the two `rankRows` tests to `map[string]floatEntry`, add three-state + day-reset + `resolveFloats` + `pollOnce` end-to-end tests, plus fake-requester / fake-Publisher / snapshot-builder helpers.
- **Modify:** `engine/internal/config/config.go` — delete the `UniverseRefreshH` field (`Scan` struct) and its default.
- **Modify:** `docs/2026-07-07-engine-pre-live-checklist.md` — remove the "Blocking for the scanner feature" item on landing.

No files are created. `cmd/etape/main.go:387` (`scan.New(cfg.Scan, client, hub, clk).Run(ctx)`) is unaffected — `New`'s signature does not change.

---

### Task 1: Float cache + three-state `rankRows`; remove the 3215 universe path

Replaces the `universe map[string]float64` with the `floats map[string]floatEntry` cache and rewrites `rankRows` to the three-state semantics. Removes `refreshUniverse`, the `uniTick` ticker, the `filterpb` import, and the `UniverseRefreshH` usage in `Run`. `resolveFloats` is **not** added yet, so `pollOnce` filters against an empty cache (every symbol "absent" → shown with blank float; `MinChangePct`/`MinVolume` still apply). The tree compiles and `config.Scan.UniverseRefreshH` becomes unreferenced (deleted in Task 2).

**Files:**
- Modify: `engine/internal/scan/scan.go`
- Test: `engine/internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `config.Scan` (fields `Enabled`, `PremarketMs`, `RTHMs`, `MinChangePct`, `MaxFloatShares`, `MinVolume`); `wsmsg.ScannerRow{Symbol string; ChangePct, Last, FloatShares *float64; Volume int64}`; `session.PhaseAt`, `session.DayMs`; `clock.Clock`.
- Produces (relied on by later tasks):
  - `type floatEntry struct { shares float64; bad bool }`
  - `func rankRows(items []rankItem, floats map[string]floatEntry, cfg config.Scan) []wsmsg.ScannerRow`
  - `Poller` fields `floats map[string]floatEntry`, `seen map[string]map[string]bool`, `seenDay int64`
  - `func (p *Poller) resetIfNewDay(now time.Time)` (renamed from `resetSeenIfNewDay`; also clears `floats`)
  - `rankItem{Symbol string; ChangePct, Last float64; Volume int64}` (unchanged)

- [ ] **Step 1: Adapt existing `rankRows` tests + add the three-state and day-reset tests (failing)**

Replace the two existing `rankRows` tests (`TestRankRowsFloatUnitAndThresholds`, `TestRankRowsUnknownFloatIsNilNotZero`) with the three tests below. Keep `TestNewHitsSeenSet` as-is. Update the import block to add `"time"` and the `session` package.

```go
package scan

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestRankRowsThresholds(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}
	floats := map[string]floatEntry{"US.LOWF": {shares: 20_000_000}, "US.BIGF": {shares: 500_000_000}}
	items := []rankItem{
		{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}, // passes
		{Symbol: "US.BIGF", ChangePct: 20.0, Last: 8.0, Volume: 900_000}, // fails float cap
		{Symbol: "US.THIN", ChangePct: 30.0, Last: 1.0, Volume: 5_000},   // fails volume floor
		{Symbol: "US.FLAT", ChangePct: 1.0, Last: 2.0, Volume: 500_000},  // fails change threshold
	}
	rows := rankRows(items, floats, cfg)
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should pass all thresholds, got %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("float should be actual shares from cache: %+v", rows[0])
	}
	if rows[0].ChangePct == nil || *rows[0].ChangePct != 12.5 {
		t.Fatalf("changePct wrong: %+v", rows[0])
	}
}

func TestRankRowsThreeStateFloat(t *testing.T) {
	floats := map[string]floatEntry{
		"US.UNDER": {shares: 20_000_000},
		"US.OVER":  {shares: 500_000_000},
		"US.BAD":   {bad: true},
		// US.ABSENT intentionally not in the cache.
	}
	items := []rankItem{
		{Symbol: "US.UNDER", ChangePct: 12, Last: 4, Volume: 300_000},
		{Symbol: "US.OVER", ChangePct: 20, Last: 8, Volume: 900_000},
		{Symbol: "US.BAD", ChangePct: 15, Last: 3, Volume: 400_000},
		{Symbol: "US.ABSENT", ChangePct: 11, Last: 2, Volume: 250_000},
	}

	// Cap ON: OVER (known over cap) and BAD dropped; UNDER shows float; ABSENT kept, blank.
	withCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000})
	gotCap := map[string]*float64{}
	for _, r := range withCap {
		gotCap[r.Symbol] = r.FloatShares
	}
	if len(withCap) != 2 {
		t.Fatalf("cap on: want 2 rows (UNDER, ABSENT), got %d: %+v", len(withCap), withCap)
	}
	if f := gotCap["US.UNDER"]; f == nil || *f != 20_000_000 {
		t.Fatalf("UNDER float wrong: %+v", gotCap["US.UNDER"])
	}
	if f, ok := gotCap["US.ABSENT"]; !ok || f != nil {
		t.Fatalf("ABSENT must be present with nil float: ok=%v f=%v", ok, f)
	}

	// Cap OFF: nothing dropped for float; BAD shown blank, OVER shown with its float.
	noCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 0})
	got := map[string]*float64{}
	for _, r := range noCap {
		got[r.Symbol] = r.FloatShares
	}
	if len(noCap) != 4 {
		t.Fatalf("cap off: want all 4 rows, got %d: %+v", len(noCap), noCap)
	}
	if f := got["US.OVER"]; f == nil || *f != 500_000_000 {
		t.Fatalf("OVER float should show when cap off: %+v", got["US.OVER"])
	}
	if got["US.BAD"] != nil {
		t.Fatalf("BAD float must be blank (nil): %+v", got["US.BAD"])
	}
}

func TestResetIfNewDayClearsFloatCacheAndSeen(t *testing.T) {
	p := &Poller{
		floats:  map[string]floatEntry{"US.A": {shares: 1}},
		seen:    map[string]map[string]bool{"premarket": {"US.A": true}},
		seenDay: session.DayMs(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC).UnixMilli()),
	}
	p.resetIfNewDay(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)) // different ET day
	if len(p.floats) != 0 {
		t.Fatalf("float cache should clear on new day: %+v", p.floats)
	}
	if len(p.seen) != 0 {
		t.Fatalf("seen-sets should clear on new day: %+v", p.seen)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/scan/ 2>&1 | head -30`
Expected: compile failure — `floatEntry` undefined, `rankRows` signature mismatch (`map[string]floatEntry` vs `map[string]float64`), `resetIfNewDay` undefined.

- [ ] **Step 3: Rewrite `scan.go` for Task 1**

Replace the entire contents of `engine/internal/scan/scan.go` with:

```go
// Package scan is the pre-market/RTH rank scanner poller. It issues request/
// response protoIDs (3410 rank, 3203 snapshot) through the OpenD client — no
// subscription quota — and publishes scanner.rank/scanner.hit. Float is
// resolved on demand for the symbols on the rank board (3203) and cached for
// the ET day; there is no low-float "universe" (3215 never echoes float).
package scan

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol    string
	ChangePct float64
	Last      float64
	Volume    int64
}

// floatEntry is a resolved float-cache entry. bad = definitively unresolvable
// this ET day (OTC error, zero float, no equity data); absent from the map =
// unknown (transient — a snapshot merely hasn't succeeded yet).
type floatEntry struct {
	shares float64
	bad    bool
}

type Poller struct {
	cfg     config.Scan
	r       requester
	pub     Publisher
	clk     clock.Clock
	floats  map[string]floatEntry      // symbol -> resolved float; absent = unknown
	seen    map[string]map[string]bool // session -> symbol -> seen
	seenDay int64                      // ET day of the current seen-sets + float cache
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk,
		floats: map[string]floatEntry{}, seen: map[string]map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	// Poll on a short base interval; the effective cadence is session-derived.
	base := p.clk.NewTicker(time.Duration(p.cfg.PremarketMs) * time.Millisecond)
	defer base.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-base.C():
			interval := p.pollInterval(now)
			if now.Sub(last) < interval {
				continue
			}
			last = now
			p.pollOnce(ctx, now)
		}
	}
}

func (p *Poller) pollInterval(now time.Time) time.Duration {
	if session.PhaseAt(now) == session.RTH {
		return time.Duration(p.cfg.RTHMs) * time.Millisecond
	}
	return time.Duration(p.cfg.PremarketMs) * time.Millisecond
}

func (p *Poller) sessionOf(now time.Time) string {
	switch session.PhaseAt(now) {
	case session.RTH:
		return "rth"
	case session.PostMarket:
		return "afterhours"
	default:
		return "premarket"
	}
}

func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	items, err := p.fetchRank(ctx)
	if err != nil {
		return // transient; next tick retries (logging added in Task 3)
	}
	p.resetIfNewDay(now)
	rows := rankRows(items, p.floats, p.cfg)
	sess := p.sessionOf(now)
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}

// rankRows is the pure transform: apply the float cache + client-side
// thresholds. Three-state float semantics (see the design's decision table):
//   - known & over cap (cap>0): drop
//   - known: include, float shown
//   - bad & cap>0: drop; bad & cap==0: include, float blank
//   - absent (transient): include, float blank
func rankRows(items []rankItem, floats map[string]floatEntry, cfg config.Scan) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if it.ChangePct < cfg.MinChangePct {
			continue
		}
		if cfg.MinVolume > 0 && it.Volume < cfg.MinVolume {
			continue
		}
		var floatPtr *float64
		if e, ok := floats[it.Symbol]; ok {
			if e.bad {
				if cfg.MaxFloatShares > 0 {
					continue // known-bad: drop when float screening is on
				}
			} else {
				if cfg.MaxFloatShares > 0 && e.shares > cfg.MaxFloatShares {
					continue // known float exceeds the cap
				}
				fv := e.shares
				floatPtr = &fv
			}
		}
		cp, lp := it.ChangePct, it.Last
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: it.Volume,
		})
	}
	return out
}

func (p *Poller) newHits(sess string, rows []wsmsg.ScannerRow) []string {
	s := p.seen[sess]
	if s == nil {
		s = map[string]bool{}
		p.seen[sess] = s
	}
	var hits []string
	for _, r := range rows {
		if !s[r.Symbol] {
			s[r.Symbol] = true
			hits = append(hits, r.Symbol)
		}
	}
	return hits
}

// resetIfNewDay clears the seen-sets AND the float cache on the ET-day
// boundary, so overnight splits/offerings are re-resolved and bad-marks last
// at most one ET day.
func (p *Poller) resetIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]map[string]bool{}
		p.floats = map[string]floatEntry{}
	}
}

// fetchRank issues 3410 and normalizes the response to []rankItem.
func (p *Poller) fetchRank(ctx context.Context) ([]rankItem, error) {
	req := &rankpb.C2S{
		SortDir: proto.Int32(0), // descending = gainers
		Offset:  proto.Int32(0),
		Count:   proto.Int32(35),
	}
	// OpenD request messages wrap the inner C2S in a required outer
	// Request{C2S:...} (proto2 required field) — a bare C2S serializes to
	// different bytes and OpenD rejects it.
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank, &rankpb.Request{C2S: req})
	if err != nil {
		return nil, err
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 { // surface OpenD-side errors instead of looking like "0 rows"
		return nil, fmt.Errorf("rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{
			Symbol:    symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(),
			Last:      d.GetPreMarketPrice(),
			Volume:    d.GetPreMarketVolume(),
		})
	}
	return out, nil
}

// symbolOf renders a moomoo Security as eTape's "US.<code>" convention.
func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode() // US-only scope (CLAUDE.md); Market is always QotMarket_US here
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/scan/ -v 2>&1 | tail -30`
Expected: PASS — `TestRankRowsThresholds`, `TestRankRowsThreeStateFloat`, `TestResetIfNewDayClearsFloatCacheAndSeen`, `TestNewHitsSeenSet`.

- [ ] **Step 5: Verify the whole module still builds**

Run: `cd engine && go build ./...`
Expected: no output (success). `config.Scan.UniverseRefreshH` is now unreferenced but still declared — Go does not error on unused struct fields.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/scan/scan.go internal/scan/scan_test.go
git commit -m "feat(scan): float cache + three-state rankRows; drop 3215 universe"
```

---

### Task 2: Delete `UniverseRefreshH` from config

`scan.go` no longer references `UniverseRefreshH` (Task 1), so the config field and its default can be removed. Sweep verification (already confirmed during design, re-confirm in Step 1): no `.toml` files reference `universe_refresh_h`; `config.Scan` is not in `tygo.yaml` (only `internal/uihub/wsmsg` is) so no TS regen is needed; the store's `SetConfig(key, value)` is a generic JSON KV, not a typed `config.Scan` hot-reload path; and the TOML decoder is not strict (`toml.DecodeFile` with no `DisallowUnknownFields`/`MetaData.Undecoded` check), so a stale `universe_refresh_h` in a user file is silently ignored — no boot-failure risk.

**Files:**
- Modify: `engine/internal/config/config.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `config.Scan` without the `UniverseRefreshH` field.

- [ ] **Step 1: Re-confirm the sweep (read-only)**

Run:
```bash
cd /Users/earl.savadera/Projects/eTape
grep -rn "universe_refresh_h\|UniverseRefreshH" . --include="*.go" --include="*.toml" --include="*.ts" 2>/dev/null | grep -v "_test.go"
```
Expected: exactly two hits, both in `engine/internal/config/config.go` (the field and the default). No `.toml`, no `.ts`, no other `.go`. If anything else appears, stop and reconcile before deleting.

- [ ] **Step 2: Delete the field**

In `engine/internal/config/config.go`, remove the `UniverseRefreshH` line from the `Scan` struct:

```go
// Scan is the [scan] section: pre-market/RTH rank scanner + on-demand float cache.
type Scan struct {
	Enabled        bool    `toml:"enabled"`
	PremarketMs    int     `toml:"premarket_ms"`     // rank poll interval before 09:30 ET
	RTHMs          int     `toml:"rth_ms"`           // rank poll interval during RTH
	RankPages      int     `toml:"rank_pages"`       // pages of <=35 to pull per rank refresh
	MinChangePct   float64 `toml:"min_change_pct"`   // client-side gainer threshold (%)
	MaxFloatShares float64 `toml:"max_float_shares"` // float cap in ACTUAL shares (not thousands)
	MinVolume      int64   `toml:"min_volume"`       // session cumulative volume floor
}
```

(Note: the `Scan` doc-comment is updated from "+ low-float universe" to "+ on-demand float cache"; `RankPages` stays — it is a pre-existing unused field tracked separately in the spec's non-goals, out of scope here.)

- [ ] **Step 3: Delete the default**

In `Default()`, drop `UniverseRefreshH: 24` from the `Scan` literal:

```go
		Scan: Scan{
			Enabled: true, PremarketMs: 2000, RTHMs: 3000, RankPages: 2,
			MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000,
		},
```

- [ ] **Step 4: Build + test config and the whole module**

Run: `cd engine && go build ./... && go test ./internal/config/ ./internal/scan/`
Expected: PASS. `config_test.go` never referenced `UniverseRefreshH`, so it still compiles and passes.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/config/config.go
git commit -m "chore(config): remove unused scan.universe_refresh_h"
```

---

### Task 3: `resolveFloats` / `snapshotBatch` (3203) — resolve, classify, chunk, split-retry, cap

Adds the on-demand float resolution: gather cache-miss board symbols, snapshot them (3203) in ≤400-code chunks, populate the cache before filtering, with a per-poll 8-request cap and binary split-retry for the "one bad code fails the whole batch" case. Wires it into `pollOnce` and adds the rank-fetch-failure `slog.Warn`.

**Files:**
- Modify: `engine/internal/scan/scan.go`
- Test: `engine/internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `opend.ProtoQotGetSecuritySnapshot` (const `3203`); `opend.Frame{Body []byte}`; snapshot pb (`qotgetsecuritysnapshot`): `Request{C2S *C2S}`, `C2S{SecurityList []*qotcommon.Security}`, `Response.GetRetType() int32`, `Response.GetRetMsg() string`, `Response.GetS2C().GetSnapshotList() []*Snapshot`, `Snapshot.GetBasic().GetSecurity() *qotcommon.Security`, `Snapshot.GetEquityExData() *EquitySnapshotExData` (nil for non-equity), `EquitySnapshotExData.GetOutstandingShares() int64`; `qotcommon.QotMarket_QotMarket_US_Security`.
- Produces (relied on by Task 4):
  - `func (p *Poller) resolveFloats(ctx context.Context, items []rankItem)`
  - `func codeOf(symbol string) string`
  - consts `maxSnapshotReqs = 8`, `snapshotChunkSize = 400`
  - `pollOnce` now calls `resolveFloats` between `resetIfNewDay` and `rankRows`, and logs rank-fetch failures.

- [ ] **Step 1: Add fake-requester + snapshot-builder test helpers**

Append these helpers to `engine/internal/scan/scan_test.go` and extend its import block. `snapshotBasic`/`equityEx` fill every proto2 `required` field (values are dummies except `OutstandingShares`) so the production `proto.Unmarshal` succeeds.

```go
// ---- imports to add to scan_test.go ----
//   "context"
//   "fmt"
//   "google.golang.org/protobuf/proto"
//   "github.com/earlisreal/eTape/engine/internal/clock"
//   "github.com/earlisreal/eTape/engine/internal/feed/opend"
//   qotcommon ".../pb/qotcommon"
//   rankpb ".../pb/qotgetuspremarketrank"
//   snappb ".../pb/qotgetsecuritysnapshot"

// fakeReq implements the scan.requester interface with canned responses.
type fakeReq struct {
	rankResp  *rankpb.Response
	rankErr   error
	snap      func(codes []string) (*snappb.Response, error)
	snapCalls int
}

func (f *fakeReq) Request(_ context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	switch protoID {
	case opend.ProtoQotGetUSPreMarketRank:
		if f.rankErr != nil {
			return opend.Frame{}, f.rankErr
		}
		return frameOf(f.rankResp), nil
	case opend.ProtoQotGetSecuritySnapshot:
		f.snapCalls++
		var codes []string
		for _, s := range req.(*snappb.Request).GetC2S().GetSecurityList() {
			codes = append(codes, s.GetCode())
		}
		resp, err := f.snap(codes)
		if err != nil {
			return opend.Frame{}, err
		}
		return frameOf(resp), nil
	default:
		return opend.Frame{}, fmt.Errorf("unexpected protoID %d", protoID)
	}
}

func frameOf(m proto.Message) opend.Frame {
	b, _ := proto.Marshal(m)
	return opend.Frame{Body: b}
}

// capturePub records published scanner payloads.
type capturePub struct {
	ranks []wsmsg.ScannerRankPayload
	hits  []wsmsg.ScanHitPayload
}

func (c *capturePub) Publish(topic wsmsg.Topic, _ string, payload any) {
	switch topic {
	case wsmsg.TopicScannerRank:
		c.ranks = append(c.ranks, payload.(wsmsg.ScannerRankPayload))
	case wsmsg.TopicScannerHit:
		c.hits = append(c.hits, payload.(wsmsg.ScanHitPayload))
	}
}

func usSec(code string) *qotcommon.Security {
	return &qotcommon.Security{
		Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
		Code:   proto.String(code),
	}
}

// snapshotBasic fills every required SnapshotBasicData field (dummy values).
func snapshotBasic(code string) *snappb.SnapshotBasicData {
	return &snappb.SnapshotBasicData{
		Security:       usSec(code),
		Type:           proto.Int32(3),
		IsSuspend:      proto.Bool(false),
		ListTime:       proto.String("2020-01-01"),
		LotSize:        proto.Int32(1),
		PriceSpread:    proto.Float64(0.01),
		UpdateTime:     proto.String("2026-07-08 04:00:00"),
		HighPrice:      proto.Float64(1),
		OpenPrice:      proto.Float64(1),
		LowPrice:       proto.Float64(1),
		LastClosePrice: proto.Float64(1),
		CurPrice:       proto.Float64(1),
		Volume:         proto.Int64(0),
		Turnover:       proto.Float64(0),
		TurnoverRate:   proto.Float64(0),
	}
}

// equityEx fills every required EquitySnapshotExData field; only
// OutstandingShares carries meaning.
func equityEx(outstanding int64) *snappb.EquitySnapshotExData {
	return &snappb.EquitySnapshotExData{
		IssuedShares:         proto.Int64(outstanding * 2),
		IssuedMarketVal:      proto.Float64(0),
		NetAsset:             proto.Float64(0),
		NetProfit:            proto.Float64(0),
		EarningsPershare:     proto.Float64(0),
		OutstandingShares:    proto.Int64(outstanding),
		OutstandingMarketVal: proto.Float64(0),
		NetAssetPershare:     proto.Float64(0),
		EyRate:               proto.Float64(0),
		PeRate:               proto.Float64(0),
		PbRate:               proto.Float64(0),
		PeTTMRate:            proto.Float64(0),
	}
}

// snap builds a Snapshot. equity=false => no EquityExData (ETF/preferred);
// outstanding<=0 with equity=true => zero-float. Both are "bad".
func snap(code string, outstanding int64, equity bool) *snappb.Snapshot {
	s := &snappb.Snapshot{Basic: snapshotBasic(code)}
	if equity {
		s.EquityExData = equityEx(outstanding)
	}
	return s
}

func snapResp(snaps ...*snappb.Snapshot) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{SnapshotList: snaps}}
}

func snapErrResp(msg string) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(1), RetMsg: proto.String(msg)}
}

func rankResp(items ...rankItem) *rankpb.Response {
	var data []*rankpb.PreMarketRankItem
	for _, it := range items {
		data = append(data, &rankpb.PreMarketRankItem{
			Security:             usSec(codeOf(it.Symbol)),
			PreMarketChangeRatio: proto.Float64(it.ChangePct),
			PreMarketPrice:       proto.Float64(it.Last),
			PreMarketVolume:      proto.Int64(it.Volume),
		})
	}
	return &rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{DataList: data}}
}

func newTestPoller(cfg config.Scan, fr *fakeReq, pub *capturePub) *Poller {
	return New(cfg, fr, pub, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)))
}
```

- [ ] **Step 2: Write the `resolveFloats` tests (failing)**

Add to `scan_test.go`:

```go
func TestResolveFloatsClassifiesKnownAndBad(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		// KNOWN -> real float; NOEQ -> no equity data; ZERO -> zero float;
		// OMIT -> requested but absent from the response.
		return snapResp(
			snap("KNOWN", 15_000_000, true),
			snap("NOEQ", 0, false),
			snap("ZERO", 0, true),
		), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.KNOWN"}, {Symbol: "US.NOEQ"}, {Symbol: "US.ZERO"}, {Symbol: "US.OMIT"}}
	p.resolveFloats(context.Background(), items)
	if e := p.floats["US.KNOWN"]; e.bad || e.shares != 15_000_000 {
		t.Fatalf("KNOWN should resolve to 15M: %+v", e)
	}
	for _, s := range []string{"US.NOEQ", "US.ZERO", "US.OMIT"} {
		if e, ok := p.floats[s]; !ok || !e.bad {
			t.Fatalf("%s should be marked bad: %+v ok=%v", s, e, ok)
		}
	}
}

func TestResolveFloatsTransportErrorLeavesAbsent(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	p.resolveFloats(context.Background(), []rankItem{{Symbol: "US.A"}})
	if _, ok := p.floats["US.A"]; ok {
		t.Fatalf("transport error must leave the symbol absent, not cached")
	}
}

func TestResolveFloatsSplitRetryIsolatesBadCode(t *testing.T) {
	// Any batch containing BAD errors as a whole until BAD is alone.
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		for _, c := range codes {
			if c == "BAD" {
				return snapErrResp("US OTC market quote is not available"), nil
			}
		}
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 10_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.B"}, {Symbol: "US.BAD"}, {Symbol: "US.C"}}
	p.resolveFloats(context.Background(), items)
	if e := p.floats["US.BAD"]; !e.bad {
		t.Fatalf("US.BAD should be isolated and marked bad: %+v", e)
	}
	for _, s := range []string{"US.A", "US.B", "US.C"} {
		if e := p.floats[s]; e.bad || e.shares != 10_000_000 {
			t.Fatalf("%s should resolve to 10M: %+v", s, e)
		}
	}
}

func TestResolveFloatsRequestCap(t *testing.T) {
	// Every batch fails as a whole -> pathological split explosion; must stop
	// at maxSnapshotReqs requests, leaving the rest absent.
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		return snapErrResp("all bad"), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	var items []rankItem
	for i := 0; i < 35; i++ {
		items = append(items, rankItem{Symbol: fmt.Sprintf("US.S%d", i)})
	}
	p.resolveFloats(context.Background(), items)
	if fr.snapCalls != maxSnapshotReqs {
		t.Fatalf("snapshot requests = %d, want cap %d", fr.snapCalls, maxSnapshotReqs)
	}
}

func TestResolveFloatsChunksAtCap(t *testing.T) {
	var maxBatch int
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		if len(codes) > maxBatch {
			maxBatch = len(codes)
		}
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 1_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	var items []rankItem
	for i := 0; i < 900; i++ { // 3 chunks of 400/400/100, all succeed (<8 reqs)
		items = append(items, rankItem{Symbol: fmt.Sprintf("US.S%d", i)})
	}
	p.resolveFloats(context.Background(), items)
	if maxBatch > snapshotChunkSize {
		t.Fatalf("a batch of %d exceeds the chunk cap %d", maxBatch, snapshotChunkSize)
	}
}

func TestResolveFloatsSteadyStateNoRequests(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 10_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.B"}}
	p.resolveFloats(context.Background(), items)
	first := fr.snapCalls
	if first == 0 {
		t.Fatalf("first resolve should have issued at least one request")
	}
	p.resolveFloats(context.Background(), items) // all cached now
	if fr.snapCalls != first {
		t.Fatalf("second resolve should issue no new requests: %d -> %d", first, fr.snapCalls)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd engine && go test ./internal/scan/ 2>&1 | head -20`
Expected: compile failure — `resolveFloats`, `codeOf`, `maxSnapshotReqs`, `snapshotChunkSize` undefined (and `snappb` import unused in `scan.go` yet).

- [ ] **Step 4: Add the implementation to `scan.go`**

**4a.** Extend the import block — add `"log/slog"` and `"strings"` to the std group, and `snappb` to the pb group:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
)
```

**4b.** Replace `pollOnce` (add the rank-failure warn and the `resolveFloats` call):

```go
func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	items, err := p.fetchRank(ctx)
	if err != nil {
		slog.Warn("scan: rank fetch failed", "err", err)
		return // transient; next tick retries
	}
	p.resetIfNewDay(now)
	p.resolveFloats(ctx, items) // populate the float cache before filtering
	rows := rankRows(items, p.floats, p.cfg)
	sess := p.sessionOf(now)
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}
```

**4c.** Add the resolution code. Insert after `fetchRank` (before `symbolOf`):

```go
const (
	maxSnapshotReqs   = 8   // per-poll 3203 request budget (backstop for the empty-cache day-reset case)
	snapshotChunkSize = 400 // 3203 codes-per-request cap
)

// resolveFloats snapshots (3203) the rank symbols not already in the float
// cache and records the results, so rankRows filters against fresh data. It
// is bounded to maxSnapshotReqs requests per poll; symbols left unresolved
// stay absent and are retried on the next poll. Steady state is zero requests
// (board symbols persist cached poll-to-poll).
func (p *Poller) resolveFloats(ctx context.Context, items []rankItem) {
	var missing []string
	for _, it := range items {
		if _, ok := p.floats[it.Symbol]; !ok {
			missing = append(missing, it.Symbol)
		}
	}
	reqs := 0
	for start := 0; start < len(missing); start += snapshotChunkSize {
		end := start + snapshotChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		p.snapshotBatch(ctx, missing[start:end], &reqs)
	}
}

// snapshotBatch resolves one batch of symbols via a single 3203 request,
// recursing with a binary split when OpenD errors the whole batch (the "one
// bad code fails the batch" case — e.g. an OTC code without quote rights).
// *reqs tracks the per-poll request budget across chunks and recursion.
func (p *Poller) snapshotBatch(ctx context.Context, syms []string, reqs *int) {
	if len(syms) == 0 {
		return
	}
	if *reqs >= maxSnapshotReqs {
		return // budget exhausted; leave the rest absent for the next poll
	}
	*reqs++

	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: secs}})
	if err != nil {
		// Transport/context error: leave symbols absent; the next poll retries.
		slog.Warn("scan: snapshot transport failed", "err", err, "n", len(syms))
		return
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("scan: snapshot decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		// Application error — the whole batch failed. Isolate the offending
		// code by binary split; a single failing code is marked bad.
		if len(syms) == 1 {
			p.floats[syms[0]] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", syms[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(syms) / 2
		p.snapshotBatch(ctx, syms[:mid], reqs)
		p.snapshotBatch(ctx, syms[mid:], reqs)
		return
	}
	// Success: record each returned security; anything requested-but-absent is bad.
	got := make(map[string]bool, len(syms))
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		sym := symbolOf(sn.GetBasic().GetSecurity())
		got[sym] = true
		ex := sn.GetEquityExData()
		if ex == nil || ex.GetOutstandingShares() <= 0 {
			p.floats[sym] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", sym, "reason", "no equity float data")
			continue
		}
		p.floats[sym] = floatEntry{shares: float64(ex.GetOutstandingShares())}
	}
	for _, s := range syms {
		if !got[s] {
			p.floats[s] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", s, "reason", "omitted from snapshot response")
		}
	}
}

// codeOf is symbolOf's inverse: eTape "US.<code>" -> the bare moomoo code.
// US-only scope (CLAUDE.md), so the prefix is always "US.".
func codeOf(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}
```

- [ ] **Step 5: Run the scan tests**

Run: `cd engine && go test ./internal/scan/ -v 2>&1 | tail -40`
Expected: PASS — all `TestResolveFloats*` plus the Task 1 tests.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/scan/scan.go internal/scan/scan_test.go
git commit -m "feat(scan): on-demand 3203 float resolution with split-retry + cap"
```

---

### Task 4: `pollOnce` end-to-end test; remove the checklist blocker; full verification

Proves the whole poll flow (rank → snapshot → filter → publish) with fakes, closes the pre-live-checklist blocking item, and runs full verification. No production-code changes.

**Files:**
- Test: `engine/internal/scan/scan_test.go`
- Modify: `docs/2026-07-07-engine-pre-live-checklist.md`

**Interfaces:**
- Consumes everything produced by Tasks 1 and 3 (via the helpers added in Task 3, Step 1).
- Produces: nothing new.

- [ ] **Step 1: Write the end-to-end test (failing until it compiles/runs)**

Add to `scan_test.go`:

```go
func TestPollOnceEndToEnd(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(
			rankItem{Symbol: "US.LOWF", ChangePct: 12, Last: 4, Volume: 300_000},  // passes
			rankItem{Symbol: "US.BIGF", ChangePct: 20, Last: 8, Volume: 900_000},  // over float cap
			rankItem{Symbol: "US.THIN", ChangePct: 30, Last: 1, Volume: 5_000},    // under volume floor
		),
		snap: func(codes []string) (*snappb.Response, error) {
			return snapResp(
				snap("LOWF", 20_000_000, true),
				snap("BIGF", 500_000_000, true),
				snap("THIN", 1_000_000, true),
			), nil
		},
	}
	pub := &capturePub{}
	p := newTestPoller(config.Scan{Enabled: true, MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}, fr, pub)

	p.pollOnce(context.Background(), p.clk.Now())

	if len(pub.ranks) != 1 {
		t.Fatalf("want exactly one rank publish, got %d", len(pub.ranks))
	}
	rows := pub.ranks[0].Rows
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should survive (BIGF over cap, THIN under volume): %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("US.LOWF float should be resolved via 3203: %+v", rows[0])
	}
	if len(pub.hits) != 1 || pub.hits[0].Symbol != "US.LOWF" {
		t.Fatalf("want one new hit for US.LOWF: %+v", pub.hits)
	}

	// Second poll, same board: still a rank publish, but no new hit.
	p.pollOnce(context.Background(), p.clk.Now())
	if len(pub.ranks) != 2 {
		t.Fatalf("want a second rank publish, got %d", len(pub.ranks))
	}
	if len(pub.hits) != 1 {
		t.Fatalf("US.LOWF already seen -> no new hit on second poll: %+v", pub.hits)
	}
	if fr.snapCalls != 1 {
		t.Fatalf("float cache should make the second poll issue zero snapshots: snapCalls=%d", fr.snapCalls)
	}
}
```

- [ ] **Step 2: Run to verify it passes**

Run: `cd engine && go test ./internal/scan/ -run TestPollOnceEndToEnd -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 3: Remove the checklist blocking item**

In `docs/2026-07-07-engine-pre-live-checklist.md`, delete the entire `## Blocking for the scanner feature` section (the heading plus the single `- [ ] **Float-universe warm-up is a permanent no-op...**` item and its `**Why:**`/`**Fix:**` body — lines 10–27). Leave the surrounding sections (`## Design decisions needed`, `## Low-priority tracked debt`) intact.

- [ ] **Step 4: Full verification**

Run:
```bash
cd engine && go build ./... && go vet ./internal/scan/ ./internal/config/ && go test ./internal/scan/ ./internal/config/
```
Expected: no build/vet output; tests PASS. Then confirm nothing else in the tree references the removed symbols:
```bash
cd /Users/earl.savadera/Projects/eTape
grep -rn "refreshUniverse\|UniverseRefreshH\|universe_refresh_h\|filterpb\|qotstockfilter" engine/ --include="*.go" | grep -v "_test.go"
```
Expected: no output.

- [ ] **Step 5: Commit**

```bash
cd /Users/earl.savadera/Projects/eTape
git add engine/internal/scan/scan_test.go docs/2026-07-07-engine-pre-live-checklist.md
git commit -m "test(scan): pollOnce end-to-end; close scanner float blocker"
```

---

## Self-Review

**Spec coverage:**
- On-demand snapshot cache; 3215 removed → Tasks 1 (cache + remove path) & 2 (config). ✓
- `floatEntry{shares, bad}` + three-state table → Task 1 `rankRows` + `TestRankRowsThreeStateFloat`. ✓
- Cache resets on ET-day boundary (`resetSeenIfNewDay` → `resetIfNewDay`, clears floats) → Task 1. ✓
- Poll flow (fetchRank warn / resetIfNewDay / resolveFloats before filter / rankRows / publish) → Tasks 1 & 3. ✓
- Snapshot parsing (known / zero-or-missing equityExData → bad / requested-but-absent → bad) → Task 3 `snapshotBatch` + `TestResolveFloatsClassifiesKnownAndBad`. ✓
- Error handling: transport (absent, no retry) vs application `retType!=0` (binary split-retry, single bad code marked) → Task 3 + tests. ✓
- Backstop cap 8 + chunk 400 as consts → Task 3 + `TestResolveFloatsRequestCap` / `TestResolveFloatsChunksAtCap`. ✓
- Steady-state zero requests → `TestResolveFloatsSteadyStateNoRequests` + e2e second poll. ✓
- Logging (slog warn/info) → Task 3. ✓
- Config sweep (`UniverseRefreshH` delete; TOML/tygo/SetConfig/strict-decoder verified n/a) → Task 2. ✓
- Remove checklist blocker on landing → Task 4. ✓
- Non-goals (RankPages, re-snapshot-on-hit, SQLite persistence, sys.events) correctly untouched. ✓

**Placeholder scan:** No TBD/TODO/"handle edge cases"; every code step shows complete code. ✓

**Type consistency:** `floatEntry`, `floats map[string]floatEntry`, `rankRows(items, floats, cfg)`, `resolveFloats(ctx, items)`, `snapshotBatch(ctx, syms, *reqs)`, `codeOf`, `maxSnapshotReqs`, `snapshotChunkSize` are used identically across Tasks 1/3/4 and the tests. Snapshot accessors (`GetBasic().GetSecurity()`, `GetEquityExData().GetOutstandingShares()`) and constructors match the generated pb (`SecurityList`, `SnapshotList`, `RetType`, `S2C`). ✓

**Note on the cap vs "~6 requests" estimate:** the spec's "~⌈log₂35⌉ ≈ 6 extra requests" is a rough isolation-depth estimate; the hard rule is `maxSnapshotReqs = 8` counting *every* 3203 request (chunks + split-retry recursion). `TestResolveFloatsRequestCap` pins the hard cap. These are consistent, not contradictory — do not "reconcile" them by removing the cap.
