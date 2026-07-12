# Watchlist panel + demo-mode integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **On approval:** save this plan to `docs/superpowers/plans/2026-07-12-watchlist-panel.md` and commit as `docs(plans): add watchlist panel implementation plan` (project auto-commit convention). Execute in an isolated git worktree.

**Goal:** Ship a user-pinned Watchlist panel (quota-free snapshot polling, one global engine-owned list, pushed over a new `watchlist.rows` topic) plus demo-mode onboarding that auto-populates/auto-shows the watchlist with the synthetic universe, retargets open panels to synth symbols on entry, and fully reverts the pre-demo workspace on Return-to-live.

**Architecture:** The rows push *is* the list — one topic carries the full membership snapshot (~3s cadence + immediate push on mutation); there is no get-list query or client membership cache. Ownership splits along the existing boundary: the **engine** owns data (membership, validation, persistence, polling, demo seeding) via a new `engine/internal/watchlist` package + two commands + one push topic; the **UI** owns workspace shape (which panels exist, each panel's symbol) and drives demo transitions through the existing `applyWorkspace` remount seam.

**Tech Stack:** Go (engine poller/list/commands, protobuf 3203), TypeScript + React (stores, panel, pure transition planners), tygo (Go→TS wire types), dockview (panel layout), vitest (UI unit tests), Go testing (engine unit tests).

## Global Constraints

- **US stocks only** — every symbol is `US.<CODE>`; normalization uppercases and ensures the `US.` prefix.
- **Never place/modify/cancel real orders**; this feature is read-only market data (3203 snapshots) + config persistence. No `Trd_*` protocols.
- **Wire types are generated, not hand-edited on the UI side** — Go structs in `engine/internal/uihub/wsmsg/payloads.go` → `make gen-ts` → `ui/src/gen/wsmsg.ts` (drift-gated by `make gen-ts-check`; no PR CI). Never edit `ui/src/gen/wsmsg.ts` by hand.
- **Hard cap: 400 symbols** (the 3203 single-request ceiling — always one request per tick). `Add` past cap → rejecting ack.
- **Poll interval default 3s, config-tunable** (`cfg.Watchlist`). Empty list → zero 3203 calls but still publishes an (empty) snapshot each tick.
- **Hub mirror MUST serve `watchlist.rows` snapshot-on-subscribe** (same as `scanner.rank`) — late-opened panels, second windows, and the demo-entry barrier depend on it.
- **`applyWorkspace` is the only live-retarget seam** — mounted dockview panels have frozen factory closures; patching `ws.panels[i].settings` does NOT reach them. All demo transitions go through `applyWorkspace` remounts.
- Run tests with the engine's `Makefile` (`make test` / `go test ./...`) and the UI's vitest. Canvas-touching UI test files must run individually (fork-pool quirk) — not relevant here (no canvas surfaces in this feature).

---

## File Structure

**Engine (new):**
- `engine/internal/watchlist/list.go` — `watchlist.List` (membership + persistence).
- `engine/internal/watchlist/list_test.go` — list unit tests.
- `engine/internal/watchlist/poller.go` — `watchlist.Poller` (3203 batch polling + publish + Poke).
- `engine/internal/watchlist/poller_test.go` — poller unit tests (fake clock + fake requester).

**Engine (modified):**
- `engine/internal/uihub/wsmsg/payloads.go` — `WatchlistRow`, `WatchlistRowsPayload`, `WatchlistAddArgs`, `WatchlistRemoveArgs`.
- `engine/internal/uihub/wsmsg/wsmsg.go` — `TopicWatchlistRows` const + `AllTopics` map entry.
- `engine/tygo.yaml` — add `"watchlist.rows"` to the **hand-declared** TS `Topic` union in the frontmatter (`wsmsg.go` is excluded from generation, so the union does NOT auto-update).
- `engine/internal/uihub/mirror.go` — retain (`applyPub`) + replay (`snapshotFrames`) for the new topic + mirror field.
- `engine/internal/uihub/commands.go` — `WatchlistAdd`/`WatchlistRemove` cases + `watchlistCtl`/`watchlistBox` + atomic `wl` field.
- `engine/internal/uihub/api.go` — `h.cmd = cmd` back-reference set in `New`.
- `engine/internal/uihub/hub.go` — `Hub.cmd` field + `SetWatchlist` setter (mirrors `SetFeed`/`SetBackfill`'s atomic-slot pattern).
- `engine/internal/config/config.go` — `Watchlist` config struct + wiring into `Config`.
- `engine/cmd/etape/main.go` — create `List`, seed in demo branch, construct/start `Poller` in `startPollers`, `hub.SetWatchlist`.
- `ui/src/gen/wsmsg.ts` — regenerated (not hand-edited).

**UI (new):**
- `ui/src/data/WatchlistStore.ts` + `ui/src/data/WatchlistStore.test.ts`
- `ui/src/chrome/panels/WatchlistPanel.tsx`
- `ui/src/chrome/menuChrome.ts` — `MenuChrome` type + `menuChrome(palette)` adapter.
- `ui/src/chrome/demoTransition.ts` + `ui/src/chrome/demoTransition.test.ts`
- `ui/src/chrome/reannounceGate.ts` + `ui/src/chrome/reannounceGate.test.ts`

**UI (modified):**
- `ui/src/data/registry.ts` — `watchlist` store field + `routeToStore` case.
- `ui/src/chrome/panels/registry.tsx` — `PANELS["watchlist"]` + `CATALOG_ORDER`.
- `ui/src/chrome/panels/tv/TVContextMenu.tsx` — `chrome` prop widened `TvChrome` → `MenuChrome`.
- `ui/src/chrome/panels/ChartPanel.tsx` — watchlist toggle menu entry.
- `ui/src/chrome/panels/ScannerPanel.tsx` — row context menu ("Add to watchlist").
- `ui/src/chrome/AppShell.tsx` — `addPanel` wsRef alignment + demo-transition edge effect.
- `ui/src/wire/DemandRegistry.ts` (+ `DemandRegistry.test.ts`) — injected `reannounceGate`.
- `ui/src/App.tsx` — wire `reannounceGate`, pass transition signal to AppShell.

---

## Phase A — Engine data + wire contract

### Task 1: Wire types + topic registration + gen-ts

**Files:**
- Modify: `engine/internal/uihub/wsmsg/payloads.go` (beside `ScannerRow`, ~line 154)
- Modify: `engine/internal/uihub/wsmsg/wsmsg.go` (Topic consts ~13-35, `AllTopics` ~38-45)

**Interfaces:**
- Produces: `wsmsg.WatchlistRow`, `wsmsg.WatchlistRowsPayload`, `wsmsg.WatchlistAddArgs`, `wsmsg.WatchlistRemoveArgs`, `wsmsg.TopicWatchlistRows`. Consumed by Tasks 3, 4, 5, 7.

- [ ] **Step 1: Add payload structs.** In `payloads.go`, after the scanner block (~line 165), add (mirror `ScannerRow`'s nullable-numeric convention exactly):

```go
// WatchlistRow is one row of the user-pinned watchlist. Last/ChangePct are
// nil until the first successful snapshot for that symbol (ScannerRow
// convention). Not reusing ScannerRow: it carries floatShares (scanner-only),
// and coupling the types drags either's evolution onto the other.
type WatchlistRow struct {
	Symbol    string   `json:"symbol"`
	Last      *float64 `json:"last" tstype:"number | null,required"`
	ChangePct *float64 `json:"changePct" tstype:"number | null,required"`
	Volume    int64    `json:"volume"`
}

// WatchlistRowsPayload is the full-snapshot push on topic watchlist.rows.
// Symbols is the authoritative membership + order (always current); Rows may
// lag Symbols by up to one poll (mutation push / failed poll) and is keyed by
// Symbol — the panel renders dashes for a Symbol absent from Rows.
type WatchlistRowsPayload struct {
	RefreshedAt *string        `json:"refreshedAt" tstype:"string | null,required"`
	Symbols     []string       `json:"symbols"`
	Rows        []WatchlistRow `json:"rows"`
}

type WatchlistAddArgs struct {
	Symbol string `json:"symbol"`
}

type WatchlistRemoveArgs struct {
	Symbol string `json:"symbol"`
}
```

> Note: `RefreshedAt` is `*string` (RFC3339 formatted the same way scan.go:149 formats it) rather than `*time.Time` — the repo publishes pre-formatted timestamp strings. The nullable-`*string` + `tstype:"string | null,required"` precedent is `PositionRow.Venue` (`payloads.go:104`); scan's non-nullable `ScannerRankPayload.RefreshedAt` is a plain `string`. `*string` here keeps the UI staleness check a simple `Date.parse` and distinguishes "never polled" (null) from a value.

- [ ] **Step 2: Register the topic.** In `wsmsg.go`, add the const beside the others (~line 22):

```go
TopicWatchlistRows Topic = "watchlist.rows"
```

`AllTopics` (~line 39-45) is a `map[Topic]bool`, not a slice — add `TopicWatchlistRows: true` to it. **Without this, clients cannot subscribe.**

- [ ] **Step 3: Add the topic to the TS union (tygo.yaml) and regenerate.** `wsmsg.go` is excluded from tygo generation (`tygo.yaml:37`), so its `Topic` union is **hand-declared** in the tygo.yaml frontmatter (~line 47-55), not derived from the Go const. Edit `engine/tygo.yaml` to add `| "watchlist.rows"` to that union literal, then run:

```bash
cd engine && make gen-ts
```

Expected: `../ui/src/gen/wsmsg.ts` now contains `WatchlistRow`, `WatchlistRowsPayload`, `WatchlistAddArgs`, `WatchlistRemoveArgs` (generated from `payloads.go`), and `"watchlist.rows"` in the `Topic` union (from the tygo.yaml edit — this part does NOT come from `payloads.go`).

- [ ] **Step 4: Verify no drift + compile.**

```bash
cd engine && make gen-ts-check && go build ./...
```

Expected: gen-ts-check passes (generated file tracked & current); build succeeds.

- [ ] **Step 5: Commit.**

```bash
git add engine/internal/uihub/wsmsg/ engine/tygo.yaml ui/src/gen/wsmsg.ts
git commit -m "feat(watchlist): add wire types + watchlist.rows topic"
```

---

### Task 2: `watchlist.List` (membership + persistence)

**Files:**
- Create: `engine/internal/watchlist/list.go`
- Test: `engine/internal/watchlist/list_test.go`

**Interfaces:**
- Consumes: a `configStore` seam (satisfied by `*store.Store` — `GetConfig(key) (string,bool,error)`, `SetConfig(key,value)`, `Flush()`).
- Produces: `watchlist.List` with `NewList(st) (*List, error)`, `Add(symbol) (bool, error)`, `Remove(symbol) bool`, `Symbols() []string`, `Seed(symbols []string)`, `ErrFull`, and exported `Normalize(raw) string`. Consumed by Tasks 3, 5, 6.

- [ ] **Step 1: Write failing tests.** Create `list_test.go`:

```go
package watchlist

import (
	"encoding/json"
	"errors"
	"testing"
)

// fakeStore is an in-memory configStore recording Flush calls.
type fakeStore struct {
	kv      map[string]string
	flushes int
}

func newFakeStore() *fakeStore { return &fakeStore{kv: map[string]string{}} }
func (f *fakeStore) GetConfig(key string) (string, bool, error) {
	v, ok := f.kv[key]
	return v, ok, nil
}
func (f *fakeStore) SetConfig(key, value string) { f.kv[key] = value }
func (f *fakeStore) Flush()                       { f.flushes++ }

func TestNewListEmptyWhenAbsent(t *testing.T) {
	l, err := NewList(newFakeStore())
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	if got := l.Symbols(); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestAddNormalizesAndDedupes(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	added, err := l.Add("aapl")
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	if got := l.Symbols(); len(got) != 1 || got[0] != "US.AAPL" {
		t.Fatalf("normalization failed: %v", got)
	}
	added, err = l.Add("US.AAPL") // duplicate
	if err != nil || added {
		t.Fatalf("dup add: added=%v err=%v (want added=false, nil)", added, err)
	}
	if got := l.Symbols(); len(got) != 1 {
		t.Fatalf("dup grew list: %v", got)
	}
}

func TestAddPersistsAndFlushes(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("TSLA")
	if st.flushes == 0 {
		t.Fatal("Add did not Flush")
	}
	raw, ok, _ := st.GetConfig(configKey)
	if !ok {
		t.Fatal("Add did not persist")
	}
	var got []string
	_ = json.Unmarshal([]byte(raw), &got)
	if len(got) != 1 || got[0] != "US.TSLA" {
		t.Fatalf("persisted %v", got)
	}
}

func TestInsertionOrderPreservedAcrossReload(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	for _, s := range []string{"c", "a", "b"} {
		_, _ = l.Add(s)
	}
	l2, _ := NewList(st) // reload from same store
	want := []string{"US.C", "US.A", "US.B"}
	got := l2.Symbols()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order lost: want %v got %v", want, got)
		}
	}
}

func TestRemoveIdempotent(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("AAPL")
	if !l.Remove("US.AAPL") {
		t.Fatal("remove existing should be true")
	}
	if l.Remove("US.AAPL") {
		t.Fatal("remove absent should be false")
	}
	if len(l.Symbols()) != 0 {
		t.Fatal("list not empty after remove")
	}
}

func TestAddRejectsPastCap(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	l.cap = 2 // shrink for test
	_, _ = l.Add("A")
	_, _ = l.Add("B")
	_, err := l.Add("C")
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}

func TestSeedReplacesWholesale(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("OLD")
	l.Seed([]string{"US.VLCN", "US.MERI"})
	got := l.Symbols()
	if len(got) != 2 || got[0] != "US.VLCN" || got[1] != "US.MERI" {
		t.Fatalf("Seed did not replace: %v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure.**

```bash
cd engine && go test ./internal/watchlist/ -run TestList -v 2>&1 | head -20
```

Expected: compile failure (`undefined: NewList`, etc.).

- [ ] **Step 3: Implement `list.go`.**

```go
// Package watchlist owns the user-pinned symbol list: membership, symbol
// normalization, the 400-symbol cap, JSON persistence through the store's
// existing config table, and the poller that pushes quota-free 3203 snapshots
// over the watchlist.rows topic. One global list, shared across all windows.
package watchlist

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

const (
	// configKey is the store config row holding the JSON array of symbols.
	configKey = "watchlist"
	// defaultCap is the 3203 single-request ceiling — one request per tick.
	defaultCap = 400
)

// ErrFull is returned by Add when the list is at its cap.
var ErrFull = errors.New("watchlist full")

// configStore is the store surface List needs (satisfied by *store.Store).
type configStore interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	Flush()
}

// List is the in-memory membership set with write-through persistence. Safe
// for concurrent Add/Remove (conn goroutine) + Symbols (poller goroutine) +
// Seed (demo boot).
type List struct {
	st  configStore
	mu  sync.Mutex
	syms []string // insertion order; authoritative payload order
	cap  int
}

// NewList loads config key "watchlist" (a JSON string array); an absent key
// yields an empty list.
func NewList(st configStore) (*List, error) {
	l := &List{st: st, cap: defaultCap}
	raw, ok, err := st.GetConfig(configKey)
	if err != nil {
		return nil, err
	}
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &l.syms); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// Normalize uppercases and ensures the US. prefix (US-only scope). A symbol
// that already carries a market prefix (contains ".") is only uppercased.
func Normalize(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if strings.Contains(s, ".") {
		return s
	}
	return "US." + s
}

// Add normalizes and appends symbol, returning added=false for a duplicate
// (harmless no-op) and ErrFull past the cap. Persists + Flushes on a real add.
func (l *List) Add(symbol string) (bool, error) {
	sym := Normalize(symbol)
	if sym == "" {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.syms {
		if s == sym {
			return false, nil
		}
	}
	if len(l.syms) >= l.cap {
		return false, ErrFull
	}
	l.syms = append(l.syms, sym)
	l.persistLocked()
	return true, nil
}

// Remove deletes symbol if present (idempotent); persists on a real removal.
func (l *List) Remove(symbol string) bool {
	sym := Normalize(symbol)
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, s := range l.syms {
		if s == sym {
			l.syms = append(l.syms[:i], l.syms[i+1:]...)
			l.persistLocked()
			return true
		}
	}
	return false
}

// Symbols returns a copy in insertion order.
func (l *List) Symbols() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.syms))
	copy(out, l.syms)
	return out
}

// Seed replaces the whole list (demo boot: trusted synth universe, no probe).
func (l *List) Seed(symbols []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syms = l.syms[:0]
	for _, s := range symbols {
		l.syms = append(l.syms, Normalize(s))
	}
	l.persistLocked()
}

// persistLocked writes the JSON array through the store and forces a Flush so
// a mutation survives the demo flow's deliberate process re-exec. Mutations
// are a-few-per-day; Flush cost is irrelevant.
func (l *List) persistLocked() {
	b, _ := json.Marshal(l.syms)
	l.st.SetConfig(configKey, string(b))
	l.st.Flush()
}
```

- [ ] **Step 4: Run tests to verify they pass.**

```bash
cd engine && go test ./internal/watchlist/ -run TestList -v 2>&1 | tail -20
```

Expected: PASS (all `TestList*` and the others in the file).

- [ ] **Step 5: Commit.**

```bash
git add engine/internal/watchlist/list.go engine/internal/watchlist/list_test.go
git commit -m "feat(watchlist): add List (membership + persistence)"
```

---

### Task 3: `watchlist.Poller` (3203 batch polling + publish + Poke)

**Files:**
- Create: `engine/internal/watchlist/poller.go`
- Test: `engine/internal/watchlist/poller_test.go`

**Interfaces:**
- Consumes: `*watchlist.List` (Task 2); local `requester` seam (`Request(ctx, protoID, req) (opend.Frame, error)`, satisfied by `*opend.Client` and `*synth.Requester`); local `Publisher` seam (`Publish(topic, key, payload)`, satisfied by `*uihub.Hub`); `clock.Clock`.
- Produces: `Poller` with `New(list, r, pub, clk, interval) *Poller`, `Run(ctx) error`, `Poke()`. Consumed by Tasks 5 (via adapter), 6.

**Reference patterns to mirror verbatim:**
- Binary-split-and-retry: `stockinfo.snapshotChunk` (`stockinfo.go:160-185`) / `scan.snapshotBatch` (`scan.go:519-583`).
- Field mapping: `stockinfo.snapshotToPayload` (`stockinfo.go:408-426`) — `basic.GetCurPrice()`→Last, `(CurPrice-LastClosePrice)/LastClosePrice*100`→ChangePct (only when LastClosePrice != 0), `basic.GetVolume()`→Volume.
- `securitiesFor`/`codeOf`/`symbolOf` helpers (`stockinfo.go:388-397, 453-467`) — copy locally (the repo deliberately duplicates these tiny helpers per poller rather than exporting them).
- Timestamp format: `clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")` (scan.go:149).

- [ ] **Step 1: Write failing tests.** Create `poller_test.go` (fake clock + fake requester, scan_test style). Use the real `snappb` types so the transform is exercised end-to-end:

```go
package watchlist

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

type pub struct{ frames []struct {
	topic wsmsg.Topic
	key   string
	pl    any
} }

func (p *pub) Publish(topic wsmsg.Topic, key string, payload any) {
	p.frames = append(p.frames, struct {
		topic wsmsg.Topic
		key   string
		pl    any
	}{topic, key, payload})
}
func (p *pub) last() wsmsg.WatchlistRowsPayload {
	return p.frames[len(p.frames)-1].pl.(wsmsg.WatchlistRowsPayload)
}

// fakeReq returns a canned snapshot for the requested codes; retType controls
// a whole-batch application failure to exercise binary split.
type fakeReq struct {
	calls   int
	failAll bool
}

func (r *fakeReq) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	r.calls++
	in := req.(*snappb.Request)
	var resp snappb.Response
	if r.failAll {
		rt := int32(1)
		resp.RetType = &rt
		msg := "batch fail"
		resp.RetMsg = &msg
		b, _ := proto.Marshal(&resp)
		return opend.Frame{Body: b}, nil
	}
	var list []*snappb.Snapshot
	for _, sec := range in.GetC2S().GetSecurityList() {
		code := sec.GetCode()
		cur, last, vol := 10.0, 8.0, int64(1000)
		list = append(list, &snappb.Snapshot{
			Basic: &snappb.SnapshotBasicData{
				Security:       &qotcommon.Security{Code: proto.String(code), Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security))},
				CurPrice:       &cur,
				LastClosePrice: &last,
				Volume:         &vol,
			},
		})
	}
	resp.S2C = &snappb.S2C{SnapshotList: list}
	b, _ := proto.Marshal(&resp)
	return opend.Frame{Body: b}, nil
}

// Direct-call idiom (no Run/ticker involved), matching scan_test.go's
// p.pollOnce(...) and stockinfo_test.go's p.fetchSnapshots(...) style — fast,
// deterministic, no goroutine/sleep races.

func TestEmptyListPublishesButZeroRequests(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	pb := &pub{}
	r := &fakeReq{}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	if r.calls != 0 {
		t.Fatalf("empty list issued %d requests, want 0", r.calls)
	}
	if len(pb.frames) == 0 {
		t.Fatal("empty list published nothing (push-is-the-list broken)")
	}
	if len(pb.last().Symbols) != 0 {
		t.Fatalf("want empty Symbols, got %v", pb.last().Symbols)
	}
}

func TestPollComputesChangePct(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("AAPL")
	pb := &pub{}
	r := &fakeReq{}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	got := pb.last()
	if len(got.Rows) != 1 || got.Rows[0].Symbol != "US.AAPL" {
		t.Fatalf("rows=%v", got.Rows)
	}
	// (10-8)/8*100 = 25
	if got.Rows[0].ChangePct == nil || *got.Rows[0].ChangePct != 25 {
		t.Fatalf("changePct=%v want 25", got.Rows[0].ChangePct)
	}
	if got.RefreshedAt == nil {
		t.Fatal("RefreshedAt nil after successful poll")
	}
}

func TestPokePublishesMembershipImmediately(t *testing.T) {
	// Poke's "publish membership, then fresh poll" behavior lives in Run's
	// select loop, so this test drives Run with a REAL clock + a poll-until
	// deadline (stockinfo_test.go's Run-driving idiom), not the fake clock.
	st := newFakeStore()
	l, _ := NewList(st)
	pb := &pub{}
	r := &fakeReq{}
	p := New(l, r, pb, clock.System{}, time.Hour) // long interval: only Poke should drive activity
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(pb.frames) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // wait for Run's initial publishMembership
	}
	_, _ = l.Add("MSFT")
	p.Poke()
	found := false
	for time.Now().Before(deadline) && !found {
		for _, f := range pb.frames {
			pl := f.pl.(wsmsg.WatchlistRowsPayload)
			for _, s := range pl.Symbols {
				if s == "US.MSFT" {
					found = true
				}
			}
		}
		if !found {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("Poke did not publish membership including US.MSFT within deadline")
	}
}

func TestBinarySplitOnBatchFailure(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("A")
	_, _ = l.Add("B")
	pb := &pub{}
	r := &fakeReq{failAll: true}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	// 2 syms → fail → split into [A],[B] → 1 top + 2 leaves = 3 calls.
	if r.calls != 3 {
		t.Fatalf("binary split calls=%d want 3", r.calls)
	}
	// Symbols still complete even though rows are empty (all bad).
	if len(pb.last().Symbols) != 2 {
		t.Fatalf("Symbols dropped on failure: %v", pb.last().Symbols)
	}
}
```

Fake-clock API confirmed against `engine/internal/clock/fake.go`: `clock.NewFake(start time.Time) *Fake` (`fake.go:27`), `(*Fake) Advance(d)` (`fake.go:52`) — not needed by the direct-call tests above since they bypass `Run`'s ticker entirely, but `clk.Now()` on the fake clock still backs `RefreshedAt`'s timestamp.

- [ ] **Step 2: Run to verify failure.**

```bash
cd engine && go test ./internal/watchlist/ -run "TestEmpty|TestPoll|TestPoke|TestBinary" 2>&1 | head -20
```

Expected: compile failure (`undefined: New`).

- [ ] **Step 3: Implement `poller.go`.**

```go
package watchlist

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// watchlistKey is the single publish key for the one global list.
const watchlistKey = ""

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// Poller ticks on interval, polls one batched 3203 for the whole list, and
// publishes a full watchlist.rows snapshot. The Run goroutine is the ONLY
// publisher of the topic — Poke wakes it for an immediate membership push +
// fresh poll, so no mutex guards the row cache (Run-goroutine-only).
type Poller struct {
	list     *List
	r        requester
	pub      Publisher
	clk      clock.Clock
	interval time.Duration
	poke     chan struct{}
	rows     map[string]wsmsg.WatchlistRow // last-known row per symbol (cache)
	lastRef  *string                       // RFC3339 of last successful poll; nil until first
}

func New(list *List, r requester, pub Publisher, clk clock.Clock, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &Poller{
		list: list, r: r, pub: pub, clk: clk, interval: interval,
		poke: make(chan struct{}, 1),
		rows: map[string]wsmsg.WatchlistRow{},
	}
}

func (p *Poller) Run(ctx context.Context) error {
	// Publish membership immediately so the mirror + late subscribers (and the
	// demo-entry barrier) see the seeded symbols without waiting a full tick.
	p.publishMembership()
	tick := p.clk.NewTicker(p.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.pollAndPublish(ctx)
		case <-p.poke:
			p.publishMembership()  // instant: new symbols appear as dashes, removed vanish
			p.pollAndPublish(ctx)  // then fresh data
		}
	}
}

// Poke is a non-blocking wake (coalesces if a poke is already queued).
func (p *Poller) Poke() {
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

// publishMembership publishes the current membership with whatever rows are
// cached (unknown symbols render as dashes UI-side).
func (p *Poller) publishMembership() {
	syms := p.list.Symbols()
	p.pub.Publish(wsmsg.TopicWatchlistRows, watchlistKey, p.buildPayload(syms))
}

// pollAndPublish issues one batched 3203 (binary-split on batch failure),
// updates the row cache, stamps RefreshedAt, and publishes. Empty list → zero
// requests but still publishes an empty snapshot.
func (p *Poller) pollAndPublish(ctx context.Context) {
	syms := p.list.Symbols()
	if len(syms) > 0 {
		got := map[string]*snappb.Snapshot{}
		p.snapshotBatch(ctx, syms, got)
		for sym, sn := range got {
			b := sn.GetBasic()
			row := wsmsg.WatchlistRow{Symbol: sym, Volume: b.GetVolume()}
			cur, lc := b.GetCurPrice(), b.GetLastClosePrice()
			row.Last = &cur
			if lc != 0 {
				cp := (cur - lc) / lc * 100
				row.ChangePct = &cp
			}
			p.rows[sym] = row
		}
		ref := p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
		p.lastRef = &ref
	}
	p.pub.Publish(wsmsg.TopicWatchlistRows, watchlistKey, p.buildPayload(syms))
}

// buildPayload assembles the snapshot: Symbols is always the full membership;
// Rows carries only symbols with a cached row (Symbols/Rows split is
// deliberate — membership is instantly correct, rows may lag).
func (p *Poller) buildPayload(syms []string) wsmsg.WatchlistRowsPayload {
	rows := make([]wsmsg.WatchlistRow, 0, len(syms))
	live := map[string]bool{}
	for _, s := range syms {
		live[s] = true
		if r, ok := p.rows[s]; ok {
			rows = append(rows, r)
		}
	}
	// Evict cache entries for removed symbols to bound memory.
	for s := range p.rows {
		if !live[s] {
			delete(p.rows, s)
		}
	}
	return wsmsg.WatchlistRowsPayload{RefreshedAt: p.lastRef, Symbols: syms, Rows: rows}
}

// snapshotBatch resolves one batch via a single 3203, recursing with a binary
// split on a whole-batch RetType != 0 failure (lifted from
// stockinfo.snapshotChunk / scan.snapshotBatch). Probe-at-add makes this a
// delisting/edge safety net, not a hot path.
func (p *Poller) snapshotBatch(ctx context.Context, syms []string, out map[string]*snappb.Snapshot) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: securitiesFor(syms)}})
	if err != nil {
		slog.Warn("watchlist: snapshot transport failed", "err", err, "n", len(syms))
		return
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("watchlist: snapshot decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		if len(syms) == 1 {
			slog.Info("watchlist: snapshot unresolvable this tick", "symbol", syms[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(syms) / 2
		p.snapshotBatch(ctx, syms[:mid], out)
		p.snapshotBatch(ctx, syms[mid:], out)
		return
	}
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		out[symbolOf(sn.GetBasic().GetSecurity())] = sn
	}
}

func securitiesFor(syms []string) []*qotcommon.Security {
	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	return secs
}

// codeOf/symbolOf replicate stockinfo.go:457-467 locally (the repo's
// established convention: each poller keeps its own copy rather than
// exporting these two one-liners from another package).
func codeOf(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode()
}
```

Add `"strings"` to the import block.

- [ ] **Step 4: Run tests to verify they pass.**

```bash
cd engine && go test ./internal/watchlist/ -v 2>&1 | tail -25
```

Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add engine/internal/watchlist/poller.go engine/internal/watchlist/poller_test.go
git commit -m "feat(watchlist): add Poller (3203 batch polling + Poke)"
```

---

### Task 4: Hub mirror — snapshot-on-subscribe for `watchlist.rows`

**Files:**
- Modify: `engine/internal/uihub/mirror.go` (struct ~32-65; `newMirror` ~67-93; `applyPub` ~292-322; `snapshotFrames` ~339-420)

**Interfaces:**
- Consumes: `wsmsg.WatchlistRowsPayload`, `wsmsg.TopicWatchlistRows` (Task 1). No new exported API.

- [ ] **Step 1: Add the mirror field.** In the `mirror` struct's scanner/news group (~line 44), add:

```go
	watchlist    wsmsg.WatchlistRowsPayload // the one global list snapshot
	watchlistSet bool                       // false until the first publish
```

- [ ] **Step 2: Retain on publish.** In `applyPub` (~line 293 switch), add a case:

```go
	case wsmsg.TopicWatchlistRows:
		m.watchlist = s.Payload.(wsmsg.WatchlistRowsPayload)
		m.watchlistSet = true
```

- [ ] **Step 3: Replay on subscribe.** In `snapshotFrames` (~line 341 switch), add a case that coalesces nil slices to `[]` (matching the tape/bars/news precedent so a late subscriber never gets `null`):

```go
	case wsmsg.TopicWatchlistRows:
		if m.watchlistSet {
			pl := m.watchlist
			if pl.Symbols == nil {
				pl.Symbols = []string{}
			}
			if pl.Rows == nil {
				pl.Rows = []wsmsg.WatchlistRow{}
			}
			out = append(out, staged{Topic: topic, Payload: pl})
		}
```

> `newMirror` needs no change — the zero-value `wsmsg.WatchlistRowsPayload` + `watchlistSet=false` is the correct "nothing yet" state.

- [ ] **Step 4: Compile + run hub/mirror tests.**

```bash
cd engine && go build ./... && go test ./internal/uihub/... 2>&1 | tail -15
```

Expected: build succeeds; existing uihub tests pass. `mirror_test.go` uses per-topic test functions (e.g. `TestMirrorApplyPubNewsHealthEvents`, ~line 254), not a topic table — add a dedicated `TestMirrorWatchlist` that calls `applyPub(staged{Topic: TopicWatchlistRows, Payload: ...})` then asserts `snapshotFrames(TopicWatchlistRows)` returns the retained payload with non-nil slices.

- [ ] **Step 5: Commit.**

```bash
git add engine/internal/uihub/mirror.go
git commit -m "feat(watchlist): mirror watchlist.rows snapshot-on-subscribe"
```

---

### Task 5: Commands `WatchlistAdd`/`WatchlistRemove` + ctl wiring

**Files:**
- Modify: `engine/internal/uihub/commands.go` (struct ~68-92; `handle` switch ~208-231, beside `EnsureSymbol`)
- Modify: `engine/internal/uihub/api.go` (`New` ~112-120; add `h.cmd = cmd` after `newCommands`)
- Modify: `engine/internal/uihub/hub.go` (`Hub` struct — add `cmd *commands` field, ~118-160; `SetWatchlist` beside `SetFeed`/`SetBackfill`, ~227-236)
- Test: `engine/internal/uihub/commands_test.go` (add cases)

**Interfaces:**
- Consumes: the probe path (`cd.probe(ctx, symbol)`, commands.go:349), `wsmsg.WatchlistAddArgs`/`WatchlistRemoveArgs` (Task 1).
- Produces: `watchlistCtl` interface (`Add(symbol) (bool, error)`, `Remove(symbol) bool`, `Poke()`); `(*Hub).SetWatchlist(watchlistCtl)`. Consumed by Task 6.

- [ ] **Step 1: Define the ctl seam + command struct field.** In `commands.go`, near the other dep interfaces (~line 46), add:

```go
// watchlistCtl is the watchlist surface the add/remove commands drive
// (satisfied by a *watchlist.List + *watchlist.Poller adapter, wired in
// startPollers). Nil until SetWatchlist runs — guard in the handler.
type watchlistCtl interface {
	Add(symbol string) (added bool, err error)
	Remove(symbol string) (removed bool)
	Poke()
}
```

Add an **atomic** field to the `commands` struct (beside `startDemo`, ~line 91). The ctl is late-bound (created in `startPollers`, after `uihub.New` returns) and read from conn goroutines, so it must be race-free — verified via `hub.go:93-97,139-141,227-236`: `Hub.feed()`/`SetFeed` use `feedSlot atomic.Pointer[feedBox]` where `feedBox{f Feed}` boxes the interface (avoids any nil-pointer-vs-nil-interface ambiguity on `Load()`). Mirror that box pattern exactly rather than a plain field (which the race detector would flag) or a bare `atomic.Pointer[watchlistCtl]`. Import `sync/atomic`.

```go
	// wl is late-bound via (*Hub).SetWatchlist once the poller exists
	// (startPollers, after uihub.New returns), then read from conn goroutines
	// on every WatchlistAdd/Remove — same atomic-slot rationale as feedSlot
	// (hub.go:93-97). Boxed (watchlistBox) to match the feedBox precedent.
	wl atomic.Pointer[watchlistBox]
```

```go
// watchlistBox boxes watchlistCtl for atomic.Pointer storage — same reason
// feedBox boxes Feed (hub.go): an interface value can't be atomically stored
// directly, and boxing sidesteps nil-pointer-vs-nil-interface ambiguity on Load.
type watchlistBox struct{ wl watchlistCtl }
```

Add a helper on `commands` to load it (nil-safe):

```go
func (cd *commands) watchlist() watchlistCtl {
	if b := cd.wl.Load(); b != nil {
		return b.wl
	}
	return nil
}
```

- [ ] **Step 2: Add the command cases.** In `handle`'s switch, beside `EnsureSymbol` (~line 224), add:

```go
	case "WatchlistAdd":
		var a wsmsg.WatchlistAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		wl := cd.watchlist()
		if wl == nil {
			return blocked("watchlist not ready"), false
		}
		sym := watchlist.Normalize(a.Symbol)
		if !supportedMarket(sym) {
			return blocked("unsupported market"), false
		}
		if reason := cd.probe(ctx, sym); reason != "" {
			return blocked(reason), false
		}
		_, err := wl.Add(sym)
		if errors.Is(err, watchlist.ErrFull) {
			return blocked("watchlist full (400)"), false
		}
		if err != nil {
			return blocked("watchlist error"), false
		}
		wl.Poke()
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "WatchlistRemove":
		var a wsmsg.WatchlistRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		wl := cd.watchlist()
		if wl == nil {
			return blocked("watchlist not ready"), false
		}
		wl.Remove(a.Symbol) // idempotent — always accepted
		wl.Poke()
		return wsmsg.AckMsg{Status: "accepted"}, false
```

Add the `watchlist` package import (`"github.com/earlisreal/eTape/engine/internal/watchlist"`) and confirm `errors` is imported (it is — used by `probe`).

- [ ] **Step 3: Hub back-reference + setter.** The `Hub` struct does **not** currently hold a `cmd` reference (verified: `hub.go:118-160`), and `SetFeed`/`SetBackfill` reach commands via getter closures passed into `newCommands` (`api.go:113` passes `h.feed`), not a stored `cmd`. To wire the late-bound ctl without churning `newCommands`'s many test call sites, add a back-reference: add a `cmd *commands` field to the `Hub` struct (`hub.go`), and in `api.go` `New` after `cmd := newCommands(...)` (~line 113) add `h.cmd = cmd` (set once, before `h.Run`/any conn — no race on `h.cmd` itself). Then add the setter beside `SetFeed`/`SetBackfill` (`hub.go:227-236`):

```go
// SetWatchlist wires the watchlist add/remove commands once the poller exists
// (called from startPollers, after uihub.New). Stores atomically into the
// commands' wl slot — same late-binding + race-safety as SetFeed. h.cmd is set
// once in New before any goroutine, so reading it here is race-free.
func (h *Hub) SetWatchlist(c watchlistCtl) {
	if h.cmd != nil {
		h.cmd.wl.Store(&watchlistBox{wl: c})
	}
}
```

- [ ] **Step 4: Add command tests.** In `commands_test.go`, add a fake `watchlistCtl` and cases: (a) WatchlistAdd with a probe rejection → blocked ack; (b) duplicate add (ctl.Add returns added=false, nil) → accepted; (c) add past cap (ctl.Add returns ErrFull) → blocked "watchlist full (400)"; (d) WatchlistRemove → accepted + ctl.Remove + Poke called; (e) command with no ctl set → blocked "watchlist not ready" (the zero-value `cd.wl` is an unset `atomic.Pointer`, so `cd.watchlist()` returns nil without any setup — no special-casing needed for this test). Follow the existing `EnsureSymbol` test's construction of a `commands` with fakes; wire the fake via `cd.wl.Store(&watchlistBox{wl: &fakeWL{}})` (the same mechanism `SetWatchlist` uses) rather than a direct field assignment.

- [ ] **Step 5: Run + compile.**

```bash
cd engine && go build ./... && go test ./internal/uihub/... -run Watchlist -v 2>&1 | tail -20
```

Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add engine/internal/uihub/commands.go engine/internal/uihub/api.go engine/internal/uihub/hub.go engine/internal/uihub/commands_test.go
git commit -m "feat(watchlist): WatchlistAdd/Remove commands + Hub.SetWatchlist"
```

---

### Task 6: main.go wiring — List creation, demo seed, poller start, config

**Files:**
- Modify: `engine/internal/config/config.go` (add `Watchlist` struct + field, ~beside `StockInfo` line 147)
- Modify: `engine/cmd/etape/main.go` (List creation ~before line 588; demo seed ~line 597; `startPollers` signature + body ~983; call site ~700)

**Interfaces:**
- Consumes: `watchlist.NewList`, `watchlist.New`, `(*Hub).SetWatchlist`, `gen.Symbols()`.

- [ ] **Step 1: Config struct.** In `config.go`, add (mirror `StockInfo`):

```go
type Watchlist struct {
	Enabled bool `toml:"enabled"`
	PollMs  int  `toml:"poll_ms"` // 3203 poll cadence; 0 => 3000
}
```

Add `Watchlist Watchlist \`toml:"watchlist"\`` to the `Config` struct and a sensible default (`Enabled: true, PollMs: 3000`) wherever defaults are seeded (check `config.go`'s defaulting/`Default()` path and any TOML sample).

- [ ] **Step 2: Create the List (shared by demo-seed + startPollers).** In `main.go`, inside the live-or-demo branch but **before** `if *demo {` (~line 587, after the `var feedForHub ...` declarations):

```go
		wl, err := watchlist.NewList(st)
		if err != nil {
			log.Error("watchlist: load failed", "err", err)
			wl, _ = watchlist.NewList(st) // never fatal; worst case starts empty
		}
```

Add the import `"github.com/earlisreal/eTape/engine/internal/watchlist"`.

- [ ] **Step 3: Demo seed.** In the `if *demo {` block, after `gen := synth.New(...)` and its `gen.Seed(st, ...)` (~line 597), add:

```go
			wl.Seed(gen.Symbols()) // synth universe; trusted, no probe; into throwaway demo.db
```

> Ordering note (documented deviation from the spec's literal wording): the spec says seed "before the WS listener accepts connections", but in the re-exec'd `-demo` process the listener (`httpSrv.ListenAndServe`, ~line 467) starts before the demo sub-branch (~588). Race-freeness is instead guaranteed by (a) seeding before `startPollers` starts the poller (~700) and (b) the poller's initial `publishMembership()` at Run start, plus (c) the UI's WS-driven entry barrier (Task 13) which waits on the first `watchlist.rows` push with a 5s safety timeout. The "real watchlist untouched" guarantee still holds structurally — the demo process runs against its own temp `demo.db`, never the live store.

- [ ] **Step 4: startPollers — construct + start the poller + wire the ctl.** Add a `wl *watchlist.List` parameter to `startPollers` (`main.go:983`). In its body (beside the scan/stockinfo starts, ~line 984-991), add:

```go
	if cfg.Watchlist.Enabled {
		interval := time.Duration(cfg.Watchlist.PollMs) * time.Millisecond
		wp := watchlist.New(wl, r, hub, clk, interval)
		hub.SetWatchlist(watchlistAdapter{l: wl, p: wp})
		go func() { _ = wp.Run(ctx) }()
	}
```

Add the adapter type near the other main.go helpers:

```go
// watchlistAdapter satisfies uihub's watchlistCtl: Add/Remove on the List,
// Poke on the Poller.
type watchlistAdapter struct {
	l *watchlist.List
	p *watchlist.Poller
}

func (a watchlistAdapter) Add(s string) (bool, error) { return a.l.Add(s) }
func (a watchlistAdapter) Remove(s string) bool       { return a.l.Remove(s) }
func (a watchlistAdapter) Poke()                      { a.p.Poke() }
```

Update the `startPollers(...)` call site (~line 700) to pass `wl`.

> The watchlist poller runs in the live and demo paths (both reach `startPollers`). **Replay is NOT wired in v1** (the replay branch at main.go:701 does not call `startPollers`, and the replay feed exposes no 3203 requester) — the replay watchlist panel shows empty. This is consistent with the spec's "Replay mode … accepted v1 behavior … the watchlist's data columns are a live/demo feature" tradeoff. See the open question flagged at plan end.

- [ ] **Step 5: Build + smoke.**

```bash
cd engine && go build ./... && go vet ./internal/watchlist/ ./internal/uihub/ ./cmd/etape/
```

Expected: clean build. End-to-end verification happens in the final verification section (Task 14).

- [ ] **Step 6: Commit.**

```bash
git add engine/internal/config/config.go engine/cmd/etape/main.go
git commit -m "feat(watchlist): wire List + Poller into engine boot + demo seed"
```

---

## Phase B — UI data store

### Task 7: `WatchlistStore` + topic routing

**Files:**
- Create: `ui/src/data/WatchlistStore.ts`
- Test: `ui/src/data/WatchlistStore.test.ts`
- Modify: `ui/src/data/registry.ts` (`Stores` ~20-37; `makeStores` ~39-58; `routeToStore` ~60-86)

**Interfaces:**
- Consumes: `WatchlistRowsPayload`, `WatchlistRow` from `../wire/contract` (re-exported from `../gen/wsmsg`), `SnapshotMsg`/`DeltaMsg` envelopes, `ReactStore` base (`data/store.ts:19-28`).
- Produces: `WatchlistStore` with `subscribe`/`getSnapshot` (from base), `apply(m)`, `has(symbol): boolean`, and a snapshot shape `{ symbols: string[]; rows: Map<string, WatchlistRow>; refreshedAt: string | null }`. Consumed by Task 8, 13.

- [ ] **Step 1: Write failing tests.** Create `WatchlistStore.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { WatchlistStore } from "./WatchlistStore";
import type { SnapshotMsg, WatchlistRowsPayload } from "../wire/contract";

// SnapshotMsg/DeltaMsg require a `kind` discriminant (gen/wsmsg.ts) — every
// existing store test (BarStore.test.ts, ScannerStore.test.ts) includes it;
// omitting it fails typecheck, not just at runtime.
function msg(payload: WatchlistRowsPayload): SnapshotMsg {
  return { kind: "snapshot", topic: "watchlist.rows", payload };
}

describe("WatchlistStore", () => {
  it("applies a snapshot: symbols, rows map, refreshedAt", () => {
    const s = new WatchlistStore();
    s.apply(
      msg({
        refreshedAt: "2026-07-12T14:00:00.000Z",
        symbols: ["US.AAPL", "US.TSLA"],
        rows: [{ symbol: "US.AAPL", last: 10, changePct: 25, volume: 1000 }],
      }),
    );
    const snap = s.getSnapshot();
    expect(snap.symbols).toEqual(["US.AAPL", "US.TSLA"]);
    expect(snap.refreshedAt).toBe("2026-07-12T14:00:00.000Z");
    expect(snap.rows.get("US.AAPL")?.last).toBe(10);
    expect(snap.rows.has("US.TSLA")).toBe(false); // placeholder: in symbols, absent from rows
  });

  it("has() reflects membership", () => {
    const s = new WatchlistStore();
    expect(s.has("US.AAPL")).toBe(false);
    s.apply(msg({ refreshedAt: null, symbols: ["US.AAPL"], rows: [] }));
    expect(s.has("US.AAPL")).toBe(true);
    expect(s.has("US.NOPE")).toBe(false);
  });

  it("tolerates null slices from an early snapshot", () => {
    const s = new WatchlistStore();
    // A malformed/early payload shouldn't throw.
    s.apply(msg({ refreshedAt: null, symbols: null as never, rows: null as never }));
    expect(s.getSnapshot().symbols).toEqual([]);
    expect(s.has("US.X")).toBe(false);
  });
});
```

- [ ] **Step 2: Run to verify failure.**

```bash
cd ui && npx vitest run src/data/WatchlistStore.test.ts 2>&1 | tail -15
```

Expected: FAIL (module not found).

- [ ] **Step 3: Implement `WatchlistStore.ts`** (mirror `ScannerStore` minus all flash/mute/seen/hit machinery):

```ts
import { ReactStore } from "./store";
import type { DeltaMsg, SnapshotMsg, WatchlistRow, WatchlistRowsPayload } from "../wire/contract";

export interface WatchlistState {
  symbols: string[];
  rows: Map<string, WatchlistRow>;
  refreshedAt: string | null;
}

const EMPTY: WatchlistState = { symbols: [], rows: new Map(), refreshedAt: null };

// WatchlistStore holds the single global watchlist snapshot. Deliberately none
// of ScannerStore's flash/mute/seen machinery — a user-curated stable list has
// no "new hit" churn event.
export class WatchlistStore extends ReactStore<WatchlistState> {
  private membership = new Set<string>();

  constructor() {
    super(EMPTY);
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const p = m.payload as WatchlistRowsPayload;
    const symbols = p.symbols ?? [];
    const rows = new Map<string, WatchlistRow>();
    for (const r of p.rows ?? []) rows.set(r.symbol, r);
    this.membership = new Set(symbols);
    this.set({ symbols, rows, refreshedAt: p.refreshedAt ?? null });
  }

  has(symbol: string): boolean {
    return this.membership.has(symbol);
  }
}
```

- [ ] **Step 4: Register in the data registry.** In `registry.ts`: add `watchlist: WatchlistStore;` to `Stores` (~line 30), `watchlist: new WatchlistStore(),` to `makeStores()` (~line 50), the import, and a route case in `routeToStore` (~line 71):

```ts
    case "watchlist.rows": stores.watchlist.apply(m); return;
```

> No App.tsx change needed — `App.tsx:133-143` unions every catalog panel's `topics`, so once the panel (Task 8) declares `topics: ["watchlist.rows"]`, the store is globally warm.

- [ ] **Step 5: Run tests to verify pass + typecheck.**

```bash
cd ui && npx vitest run src/data/WatchlistStore.test.ts && npx tsc --noEmit 2>&1 | tail -10
```

Expected: PASS; no type errors.

- [ ] **Step 6: Commit.**

```bash
git add ui/src/data/WatchlistStore.ts ui/src/data/WatchlistStore.test.ts ui/src/data/registry.ts
git commit -m "feat(watchlist): WatchlistStore + watchlist.rows routing"
```

---

## Phase C — UI panel

### Task 8: `WatchlistPanel` + registry/catalog entry

**Files:**
- Create: `ui/src/chrome/panels/WatchlistPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (`PANELS` ~123-138; `CATALOG_ORDER` ~205-206)

**Interfaces:**
- Consumes: `PanelProps` (registry.tsx:18-52) — includes `commands: { sendCommand(name, args): Promise<AckMsg> }` (registry.tsx:25); `WatchlistStore` (Task 7); `../sortColumns` (`toggleSort`/`sortRows`/`sortIndicator` — file is at `ui/src/chrome/sortColumns.ts`, imported as `../sortColumns` like ScannerPanel:9, NOT under `panels/`); `linkGroups.focus(group, symbol)` (group is non-null → use `config.group ?? "green"`); `useTheme()` palette; `useToasts()` (`ui/src/chrome/Toast.tsx:43` → `{ push, dismiss }`, `push({ level, text })`, level `"warn"`); `TVContextMenu` + `menuChrome` (Task 9); `formatChangePct` (`chrome/format.ts:8`); `bareSymbol` (`chrome/exec/orderStatus.ts:26`).
- Produces: the `WatchlistPanel` FC + `PANELS["watchlist"]` entry (`topics: ["watchlist.rows"]`, `symbolBearing: false`, no `demand`).

- [ ] **Step 1: Implement `WatchlistPanel.tsx`.** Mirror `ScannerPanel.tsx` structure (store subscription via `useSyncExternalStore`; `COLUMNS`/`SORT_ACCESSORS`; sticky header with `sortIndicator`; row `onClick` select + `onDoubleClick` `linkGroups.focus(config.group ?? "green", symbol)`; the cell style objects `th`/`symCell`/`numCell`; sign-colored `%Chg` via `palette.up`/`palette.down`/`palette.textMuted`). Additions unique to the watchlist:

  - **Columns:** Symbol, Last, %Chg, Volume (no Float). `SORT_ACCESSORS` over the rows.
  - **Rows source:** map `snap.symbols` (authoritative order) to `snap.rows.get(sym)`; a missing row renders **dash placeholders** for Last/%Chg/Volume. Default sort = payload/insertion order (no sort state) until a header is clicked; then `sortRows`.
  - **Staleness:** compute `stale = snap.refreshedAt != null && Date.now() - Date.parse(snap.refreshedAt) > 10_000`; when stale, dim the data columns (reduced opacity) rather than blanking. Re-evaluate on a lightweight `setInterval`(~2s) tick stored in state, or gate off the next store push — a 2s interval is simplest and matches the ~3s cadence.
  - **Add affordance:** a plain `<input>` pinned in the panel body (not the ledger header). On Enter with non-empty value: `commands.sendCommand("WatchlistAdd", { symbol: value })`; clear the input on an `accepted` ack; on `blocked`, keep the value and show a warn toast with `ack.reason`. No existing panel surfaces a toast from its body (ScannerPanel uses none) — call `const toast = useToasts();` directly in `WatchlistPanel` (valid: panels render under `<ToastProvider>`, `App.tsx:162`) and `toast.push({ level: "warn", text: ack.reason ?? "rejected" })`, mirroring `AppShell.onTryDemo`'s `toast.push({ level: "danger", text: ... })` (`AppShell.tsx:195-200`).
  - **Remove affordance:** right-click a row → `TVContextMenu` (Task 9) with a single danger entry "Remove {sym} from watchlist" → `commands.sendCommand("WatchlistRemove", { symbol })`. Use `chrome={menuChrome(palette)}`.
  - **Empty state:** when `snap.symbols.length === 0`, short copy "Add a symbol to start your watchlist" + the same add input.

- [ ] **Step 2: Register the panel + catalog slot.** In `registry.tsx`, add to `PANELS`:

```tsx
  "watchlist": {
    component: WatchlistPanel,
    topics: ["watchlist.rows"],
    title: "Watchlist",
    glyph: "★",
    description: "Your pinned symbols, quote snapshots",
    symbolBearing: false,
  },
```

Insert `"watchlist"` into `CATALOG_ORDER` right after `"movers"` (~line 205).

- [ ] **Step 3: Typecheck + run the panel via the app (manual, deferred to Task 14).**

```bash
cd ui && npx tsc --noEmit 2>&1 | tail -10
```

Expected: no type errors. (Panel rendering is verified end-to-end in Task 14, not unit-tested — it's a canvas/DOM React surface.)

- [ ] **Step 4: Commit.**

```bash
git add ui/src/chrome/panels/WatchlistPanel.tsx ui/src/chrome/panels/registry.tsx
git commit -m "feat(watchlist): WatchlistPanel + catalog entry"
```

---

## Phase D — Context menus

### Task 9: `MenuChrome` generalization + adapter + three menu surfaces

**Files:**
- Create: `ui/src/chrome/menuChrome.ts`
- Modify: `ui/src/chrome/panels/tv/TVContextMenu.tsx` (props ~line 5)
- Modify: `ui/src/chrome/panels/ChartPanel.tsx` (`buildMenuItems` ~596-613)
- Modify: `ui/src/chrome/panels/ScannerPanel.tsx` (add row context menu)
- (WatchlistPanel row menu already added in Task 8.)

**Interfaces:**
- Produces: `MenuChrome` type (`{ surface, border, text, hover, down }`) + `menuChrome(palette): MenuChrome`. `TvChrome` satisfies `MenuChrome` structurally → **zero change at ChartPanel's `chrome={chrome}` call site**.

- [ ] **Step 1: Create `menuChrome.ts`.**

```ts
import type { Palette } from "../render/palette";

// MenuChrome is the 5-field structural subset TVContextMenu actually reads
// (verified: surface, border, text, hover, down). TvChrome satisfies it
// structurally, so chart callers pass their existing chrome unchanged.
export interface MenuChrome {
  surface: string;
  border: string;
  text: string;
  hover: string;
  down: string; // danger-entry text color
}

// menuChrome adapts the app Palette (which has no `hover` token) for non-chart
// context-menu callers. hover is synthesized from borderStrong; danger text
// maps to palette.danger.
export function menuChrome(palette: Palette): MenuChrome {
  return {
    surface: palette.surface,
    border: palette.border,
    text: palette.text,
    hover: palette.borderStrong,
    down: palette.danger,
  };
}
```

> Confirm `palette.borderStrong` and `palette.danger` exist (recon: both are in `Palette`). If a subtler hover is wanted, use a translucent overlay instead — but `borderStrong` is a safe, existing token.

- [ ] **Step 2: Widen TVContextMenu's prop.** In `TVContextMenu.tsx`, change the import/type so `chrome: TvChrome` becomes `chrome: MenuChrome` in `TVContextMenuProps`. The component body already reads only `surface`/`border`/`text`/`hover`/`down`, so no body change. Keep the `TV_FONT`/`TV_GEOM` imports.

- [ ] **Step 3: Chart toggle entry.** In `ChartPanel.tsx` `buildMenuItems`, add (before the final `Settings…` block) a watchlist toggle that reads `stores.watchlist.has(sym)` synchronously at menu-open, using the chart's already-resolved symbol (`linkGroups.symbolFor(groupRef.current) ?? symbol` — the same `chartSymbol`/resolution the panel already computes):

```ts
  const sym = chartSymbol; // already resolved (link-group or settings.symbol)
  const inWatch = stores.watchlist.has(sym);
  items.push("separator");
  items.push(
    inWatch
      ? { label: `Remove ${bareSymbol(sym)} from watchlist`, danger: true,
          onClick: () => void commands.sendCommand("WatchlistRemove", { symbol: sym }) }
      : { label: `Add ${bareSymbol(sym)} to watchlist`,
          onClick: () => void commands.sendCommand("WatchlistAdd", { symbol: sym }) },
  );
```

ChartPanel's existing `chrome={chrome}` (a `TvChrome`) still satisfies the widened prop — no call-site change. `stores` and `commands` are already destructured in ChartPanel (`ChartPanel.tsx:115`), but **`bareSymbol` is NOT currently imported** — add `import { bareSymbol } from "../exec/orderStatus";` to ChartPanel.

- [ ] **Step 4: Scanner/Movers row menu.** In `ScannerPanel.tsx`, add `commands` to the component's prop destructure (`ScannerPanel.tsx:51` — currently `{ config, stores, linkGroups, onConfigChange, variant }`, does NOT include `commands` yet). Then add `onContextMenu` to the row `<tr>` (currently only `onClick`/`onDoubleClick`) that opens a `TVContextMenu` with a single unconditional idempotent entry "Add {sym} to watchlist" → `commands.sendCommand("WatchlistAdd", { symbol: r.symbol })`. Use `chrome={menuChrome(palette)}` (ScannerPanel uses the app palette, not TvChrome). Add the small `menu` state + `<TVContextMenu ... />` render (copy the shape from ChartPanel's `menu` state/handler). This one implementation covers both `variant: "scanner" | "movers"`.

- [ ] **Step 5: Typecheck.**

```bash
cd ui && npx tsc --noEmit 2>&1 | tail -10
```

Expected: no type errors (the structural-subset widening compiles; ChartPanel unchanged at its call site).

- [ ] **Step 6: Commit.**

```bash
git add ui/src/chrome/menuChrome.ts ui/src/chrome/panels/tv/TVContextMenu.tsx ui/src/chrome/panels/ChartPanel.tsx ui/src/chrome/panels/ScannerPanel.tsx
git commit -m "feat(watchlist): MenuChrome + add/remove context menus (chart, scanner, watchlist)"
```

---

## Phase E — Demo transition orchestration

### Task 10: `demoTransition.ts` pure planners

**Files:**
- Create: `ui/src/chrome/demoTransition.ts`
- Test: `ui/src/chrome/demoTransition.test.ts`

**Interfaces:**
- Consumes: `Workspace`, `PanelConfig`, `LinkGroup` types (`chrome/workspace.ts`, `chrome/linkGroups.ts`). Does **NOT** import `PANELS` — that would transitively pull every React panel component into this module and break its purity (unlike `typeToLoad.ts`, which imports nothing). Instead the caller passes an `isSymbolBearing: (panelId: string) => boolean` predicate.
- Produces: pure functions `planDemoEntry(current, universe, isSymbolBearing): Workspace` and `planDemoRevert(ctx, current): Workspace`, plus `interface DemoContext { snapshot: Workspace | null; universe: string[] }`. No React/DOM/WS imports. Consumed by Task 13 (AppShell passes `(id) => PANELS[id]?.symbolBearing ?? false`).

**Design (from spec §Demo transition orchestration):**
- `planDemoEntry`: sort `universe`; deterministically rewrite the four fixed link-group focus entries — `green→uni[0]`, `red→uni[1]`, `blue→uni[2]`, `yellow→uni[3]` — regardless of whether the doc uses them (window-agnostic, so every window computes the same map; guard indices against a short universe). Then cycle `uni[4:]` across **pinned** symbol-bearing panels — those with `group == null` (a grouped panel resolves via its group focus, not `settings.symbol`, so it's driven by the groups rewrite above) **and** `isSymbolBearing(panelId)` — in **stable panel-id order** (sort by `id`), wrapping if there are more panels than remaining symbols. Return the patched `Workspace` (new `groups` map + patched `panels[i].settings.symbol`); `layout` copied through unchanged (the watchlist panel is appended separately by AppShell, not baked into `layout`).
- `planDemoRevert(ctx, current)`: if `ctx.snapshot` non-null → return `ctx.snapshot` verbatim (exact restore). Else fallback: clone `current`; for every panel whose `settings.symbol ∈ ctx.universe`, set it to the default seed `"US.AAPL"`; for every `groups` entry whose symbol `∈ ctx.universe`, replace with `"US.AAPL"`. (Never leaves a fictional symbol wedged; never writes demo state into the real DB as "restored".)

- [ ] **Step 1: Write failing tests** (table-driven, covering the spec's enumerated cases):

```ts
import { describe, expect, it } from "vitest";
import { planDemoEntry, planDemoRevert } from "./demoTransition";
import type { Workspace } from "./workspace";

const UNI = ["US.AAA", "US.BBB", "US.CCC", "US.DDD", "US.EEE", "US.FFF"];
const isSymbolBearing = (panelId: string) => panelId === "chart" || panelId === "tape";

function ws(panels: Workspace["panels"], groups?: Workspace["groups"]): Workspace {
  return { name: "test", panels, layout: null, groups };
}

describe("planDemoEntry", () => {
  it("rewrites all four fixed group focus entries over the sorted universe", () => {
    const next = planDemoEntry(ws([]), UNI, isSymbolBearing);
    expect(next.groups).toEqual({ green: "US.AAA", red: "US.BBB", blue: "US.CCC", yellow: "US.DDD" });
  });

  it("cycles uni[4:] across pinned symbol-bearing panels in id order", () => {
    const next = planDemoEntry(
      ws([
        { id: "chart-2", panelId: "chart", group: null, settings: { symbol: "US.OLD" } },
        { id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.OLD" } },
      ]),
      UNI,
      isSymbolBearing,
    );
    const byId = Object.fromEntries(next.panels.map((p) => [p.id, p.settings.symbol]));
    expect(byId["chart-1"]).toBe("US.EEE"); // uni[4], id-sorted first
    expect(byId["chart-2"]).toBe("US.FFF"); // uni[5]
  });

  it("wraps when more pinned panels than remaining universe", () => {
    const panels = ["a", "b", "c"].map((s) => ({ id: `chart-${s}`, panelId: "chart", group: null, settings: { symbol: "US.OLD" } }));
    const next = planDemoEntry(ws(panels), ["US.AAA", "US.BBB", "US.CCC", "US.DDD", "US.EEE"], isSymbolBearing); // only uni[4:]=[EEE]
    const syms = next.panels.map((p) => p.settings.symbol);
    expect(syms).toEqual(["US.EEE", "US.EEE", "US.EEE"]); // all wrap to the single remaining
  });

  it("leaves a grouped panel's own settings.symbol untouched but sets its group focus", () => {
    // A panel following a link group (group !== null) resolves via groups, not
    // settings.symbol, so planDemoEntry must skip it in the pinned-panel cycle
    // and only rewrite groups deterministically.
    const next = planDemoEntry(ws([{ id: "tape-1", panelId: "tape", group: "green", settings: { symbol: "US.OLD" } }]), UNI, isSymbolBearing);
    expect(next.groups?.green).toBe("US.AAA");
    expect(next.panels[0].settings.symbol).toBe("US.OLD"); // untouched — grouped, not pinned
  });

  it("skips panels whose panelId is not symbol-bearing", () => {
    const next = planDemoEntry(
      ws([{ id: "scanner-1", panelId: "scanner", group: null, settings: {} }]),
      UNI,
      isSymbolBearing,
    );
    expect(next.panels[0].settings.symbol).toBeUndefined();
  });
});

describe("planDemoRevert", () => {
  const snapshot = ws([{ id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.NVDA" } }], { green: "US.NVDA" });

  it("with snapshot returns it verbatim", () => {
    const out = planDemoRevert({ snapshot, universe: UNI }, ws([]));
    expect(out).toEqual(snapshot);
  });

  it("without snapshot patches universe symbols + group entries to the default seed", () => {
    const current = ws(
      [
        { id: "chart-1", panelId: "chart", group: null, settings: { symbol: "US.AAA" } }, // ∈ universe
        { id: "chart-2", panelId: "chart", group: null, settings: { symbol: "US.REAL" } }, // not in universe
      ],
      { green: "US.BBB", red: "US.KEEP" },
    );
    const out = planDemoRevert({ snapshot: null, universe: UNI }, current);
    expect(out.panels[0].settings.symbol).toBe("US.AAPL");
    expect(out.panels[1].settings.symbol).toBe("US.REAL");
    expect(out.groups?.green).toBe("US.AAPL");
    expect(out.groups?.red).toBe("US.KEEP");
  });
});
```

- [ ] **Step 2: Run to verify failure.**

```bash
cd ui && npx vitest run src/chrome/demoTransition.test.ts 2>&1 | tail -15
```

Expected: FAIL (module not found).

- [ ] **Step 3: Implement `demoTransition.ts`** (pure; import only `Workspace`/`PanelConfig` types from `./workspace` and `LinkGroup` from `./linkGroups` — no `PANELS` import, `isSymbolBearing` is a caller-supplied predicate). Use `structuredClone` for defensive copies. Default seed constant `"US.AAPL"`. Sort universe with `[...universe].sort()`; a panel is "pinned symbol-bearing" when `p.group === null && isSymbolBearing(p.panelId)`; sort those by `id` before cycling `uni[4:]` (wrap with `%`). The four groups always written: `{ green: uni[0], red: uni[1], blue: uni[2], yellow: uni[3] }`, guarding against a short universe (skip an entry if the index is out of range).

- [ ] **Step 4: Run tests to verify pass.**

```bash
cd ui && npx vitest run src/chrome/demoTransition.test.ts 2>&1 | tail -15
```

Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add ui/src/chrome/demoTransition.ts ui/src/chrome/demoTransition.test.ts
git commit -m "feat(watchlist): pure demo-transition planners"
```

---

### Task 11: `addPanel` wsRef alignment (AppShell)

**Files:**
- Modify: `ui/src/chrome/AppShell.tsx` (`addPanel` ~317-332)

**Interfaces:** No API change — behavioral alignment so `addPanel` composes with `applyWorkspace` in the same tick.

- [ ] **Step 1: Align `addPanel` to the wsRef discipline.** Change `addPanel` to read/write `wsRef.current` like every sibling mutator (`onConfigChange`/`onGroupChange`/`removePanel`):

```ts
  const addPanel = (panelId: string) => {
    const def = PANELS[panelId];
    if (!def) return;
    const id = `${panelId}-${crypto.randomUUID().slice(0, 8)}`;
    const settings: Record<string, unknown> = panelId === "chart" ? { symbol: "US.AAPL", timeframe: "1m" } : {};
    const config: PanelConfig = { id, panelId, group: null, settings };
    const current = wsRef.current ?? ws;
    const next = { ...current, panels: [...current.panels, config] };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => {
        if (!api.getPanel(id)) api.addPanel({ id, component: id, title: def.title });
      });
    }
    setAddOpen(false);
  };
```

Only the `current`/`next` derivation and the added `wsRef.current = next;` change; the pending-queue push is unchanged. This lets Task 13 call `applyWorkspace(patchedDoc)` then `addPanel("watchlist")` in the same tick — the second read sees the first's `wsRef.current`.

- [ ] **Step 2: Typecheck + confirm no regression in existing add-panel flow (manual in Task 14).**

```bash
cd ui && npx tsc --noEmit 2>&1 | tail -5
```

- [ ] **Step 3: Commit.**

```bash
git add ui/src/chrome/AppShell.tsx
git commit -m "refactor(appshell): align addPanel to wsRef discipline"
```

---

### Task 12: `DemandRegistry` reannounce gate

**Files:**
- Create: `ui/src/chrome/reannounceGate.ts` + `ui/src/chrome/reannounceGate.test.ts`
- Modify: `ui/src/wire/DemandRegistry.ts` (constructor ~32-36; `reannounce` ~71-75)
- Test: `ui/src/wire/DemandRegistry.test.ts` (add cases)

**Interfaces:**
- Produces: `ReannounceGate` (tracks last-known mode; `gate(): Promise<void>` for injection, `onSessionMode(mode)`, `onTransitionApplied()`); `DemandRegistry` gains an optional `reannounceGate: () => Promise<void>` constructor arg (default `() => Promise.resolve()` for back-compat). Consumed by Task 13 (App.tsx wiring).

**Design (spec §Reannounce gating):** On socket open, `reannounce` awaits the gate before re-sending `EnsureSymbol`s. The gate resolves:
- **unchanged mode** (normal restart / WS blip): on the first `sys.session` snapshot after open (one round-trip).
- **changed mode** (demo boundary): when AppShell signals transition-applied, with a ~5s safety timeout so a panel-less client never deadlocks.

- [ ] **Step 1: Write failing tests for `ReannounceGate`.** Create `reannounceGate.test.ts` (use fake timers for the timeout path):

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { ReannounceGate } from "./reannounceGate";

afterEach(() => vi.useRealTimers());

describe("ReannounceGate", () => {
  it("unchanged mode resolves on the next session snapshot", async () => {
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("live"); // unchanged
    await expect(p).resolves.toBeUndefined();
  });

  it("changed mode waits for transition-applied", async () => {
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("demo"); // changed → must wait
    let resolved = false;
    void p.then(() => (resolved = true));
    await Promise.resolve();
    expect(resolved).toBe(false);
    g.onTransitionApplied();
    await expect(p).resolves.toBeUndefined();
  });

  it("changed mode times out if transition never signals", async () => {
    vi.useFakeTimers();
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("demo");
    vi.advanceTimersByTime(5000);
    await expect(p).resolves.toBeUndefined();
  });
});
```

- [ ] **Step 2: Run to verify failure, then implement `reannounceGate.ts`** with the described state machine: `gate()` returns a promise stored as pending; `onSessionMode(mode)` compares to `lastMode` (resolve immediately if equal, else start a timeout and wait for `onTransitionApplied()`); `onTransitionApplied()` resolves the pending changed-mode promise and clears the timer; always update `lastMode`. Guard against a `gate()` called with no pending session signal yet (queue it).

- [ ] **Step 3: Add gate to DemandRegistry.** Change the constructor to accept an optional gate and make `reannounce` async:

```ts
  constructor(
    private readonly client: DemandClient,
    private readonly reannounceGate: () => Promise<void> = () => Promise.resolve(),
  ) {
    this.client.onState((s) => {
      if (s === "open") void this.reannounce();
    });
  }

  private async reannounce(): Promise<void> {
    await this.reannounceGate();
    for (const [panelId, { symbol, profile }] of this.live) {
      void this.client.sendCommand("EnsureSymbol", { demandId: panelId, symbol, profile });
    }
  }
```

- [ ] **Step 4: Add DemandRegistry tests.** In `DemandRegistry.test.ts`, add: (a) gate defers reannounce (a never-resolving gate → no `EnsureSymbol` sent); (b) once gate resolves → all live demands re-announced; (c) default gate (no arg) preserves today's immediate reannounce. Use a controllable deferred as the injected gate.

- [ ] **Step 5: Run tests + typecheck.**

```bash
cd ui && npx vitest run src/chrome/reannounceGate.test.ts src/wire/DemandRegistry.test.ts && npx tsc --noEmit 2>&1 | tail -10
```

Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add ui/src/chrome/reannounceGate.ts ui/src/chrome/reannounceGate.test.ts ui/src/wire/DemandRegistry.ts ui/src/wire/DemandRegistry.test.ts
git commit -m "feat(watchlist): reannounce gate defers cross-mode EnsureSymbols"
```

---

### Task 13: AppShell demo edge-effect + App.tsx gate wiring

**Files:**
- Modify: `ui/src/chrome/AppShell.tsx` (add a `prevModeRef` + mode-edge effect; capture snapshot; run planners; auto-add panel; signal transition-applied)
- Modify: `ui/src/App.tsx` (construct a `ReannounceGate`, pass its `gate` to `DemandRegistry`, feed `onSessionMode` from the session subscription, pass `onTransitionApplied` down to AppShell)

**Interfaces:**
- Consumes: `planDemoEntry`/`planDemoRevert`/`DemoContext` (Task 10 — AppShell passes `(id) => PANELS[id]?.symbolBearing ?? false` as the `isSymbolBearing` arg, since `PANELS` is already imported in AppShell), `applyWorkspace`/`addPanel` (Task 11), `stores.watchlist` (Task 7), `stores.session`, `ReannounceGate` (Task 12).
- Produces: a new `onTransitionApplied?: () => void` prop threaded into `AppShellProps` (current destructure at `AppShell.tsx:72` has no such prop yet — add it).

**Design (spec §Entry/Revert/Reannounce):**
- A module-level (survives the demo relaunch — the tab never reloads) or `useRef` `snapshotRef: Workspace | null` holds the pre-demo doc.
- `prevModeRef` tracks the last `sessionMode.mode`. An effect keyed on `sessionMode.mode` fires the planners on these edges:
  - `live→demo` / `replay→demo`: **entry with snapshot** — capture `snapshotRef.current = structuredClone(wsRef.current)`, then run entry.
  - `pending→demo`: **entry, no snapshot** (`snapshotRef.current = null`).
  - `demo→live`: **revert** — `planDemoRevert({ snapshot: snapshotRef.current, universe }, wsRef.current)`.
  - `demo→demo` / any non-edge: no-op (preserves mid-demo user symbol changes).
- **Entry barrier:** gate on `stores.watchlist.getSnapshot().symbols.length > 0` — subscribe to the watchlist store and proceed on the first non-empty snapshot; a ~5s safety timeout proceeds anyway (leave panels as-is). `universe = stores.watchlist.getSnapshot().symbols`.
- **Entry apply:** `applyWorkspace(planDemoEntry(wsRef.current, universe, (id) => PANELS[id]?.symbolBearing ?? false))`, then `if (!wsRef.current.panels.some(p => p.panelId === "watchlist")) addPanel("watchlist")` — appended separately so dockview computes grid placement (no auto-added bookkeeping; revert-with-snapshot drops it because it isn't in the snapshot). Call `onTransitionApplied()` after applying.
- **Revert apply:** `applyWorkspace(planDemoRevert(...))`; call `onTransitionApplied()`.

- [ ] **Step 1: Add snapshot ref + prevMode ref + universe accessor.** In AppShell, add a module-level `let demoSnapshot: Workspace | null = null;` (survives relaunch) OR a `useRef` initialized once; and `const prevModeRef = useRef(sessionMode.mode);`. Subscribe to `stores.watchlist` via `useSyncExternalStore` so the barrier can observe symbol arrival.

- [ ] **Step 2: Add the mode-edge effect.** Add a `useEffect` keyed on `sessionMode.mode` that reads `prev = prevModeRef.current`, computes the edge, and runs the entry/revert logic per the design above. Implement the entry barrier as: if `stores.watchlist.getSnapshot().symbols.length > 0` proceed immediately; else register a one-shot store subscription + a 5s `setTimeout`, whichever fires first, then proceed. Update `prevModeRef.current = sessionMode.mode` at the end (or immediately for non-edges). Guard re-entrancy (a ref flag so a slow barrier doesn't double-fire if mode flips again).

- [ ] **Step 3: Wire the gate in App.tsx.** In the `useMemo` singletons block (`App.tsx:96-123`): `const reannounceGate = new ReannounceGate({ timeoutMs: 5000, initialMode: "pending" });` and construct `const demandRegistry = new DemandRegistry(client, () => reannounceGate.gate());`. App.tsx has **no existing `sys.session` mode subscription** (`stores.session` is only reached via `connectStores`'s generic topic routing, not observed directly in App.tsx) — add one: `useEffect(() => stores.session.subscribe(() => reannounceGate.onSessionMode(stores.session.getSnapshot().mode)), [stores.session, reannounceGate]);`. Add the new `onTransitionApplied={() => reannounceGate.onTransitionApplied()}` prop to the `<AppShell .../>` call site, and add `onTransitionApplied?: () => void` to `AppShellProps` (`AppShell.tsx:72`'s destructure), calling it after each `applyWorkspace(...)` inside the Task 13 Step 2 effect.

- [ ] **Step 4: Typecheck + build.**

```bash
cd ui && npx tsc --noEmit && npx vitest run 2>&1 | tail -15
```

Expected: no type errors; all UI unit tests pass.

- [ ] **Step 5: Commit.**

```bash
git add ui/src/chrome/AppShell.tsx ui/src/App.tsx
git commit -m "feat(watchlist): demo entry/revert orchestration + reannounce gate wiring"
```

---

## Task 14: End-to-end verification

**Goal:** Exercise the whole feature against a running engine + UI, per `superpowers:verification-before-completion` and the project `/run` + `/verify` skills. Use an **isolated config + DB** so verification never touches Earl's real `~/.eTape/etape.db` (see the config `db_path` isolation gotcha — pass `-config` AND set `store.db_path` explicitly).

- [ ] **Step 1: Full test + build gate.**

```bash
cd engine && go test ./... && make gen-ts-check && go build ./...
cd ../ui && npx tsc --noEmit && npx vitest run
```

Expected: all green; no gen-ts drift.

- [ ] **Step 2: Live/normal mode — watchlist basics.** Launch the engine (isolated config/DB) + UI. Open the Watchlist panel from the catalog (after Movers). Verify: empty state shows the add input; typing a symbol + Enter adds a row (appears within ~1 poke, dashes → data within ~3s); %Chg is sign-colored; sorting by each column works; double-click loads the symbol into the green group; right-click → "Remove from watchlist" removes it; a bad symbol Enter → warn toast, input retained. Reload the page → the list persists (config round-trip). Open a second window (`?workspace=`) → the same list appears (snapshot-on-subscribe). Add in one window → appears in the other within a poke.

- [ ] **Step 3: Chart + scanner menu entries.** Right-click a chart → "Add {sym} to watchlist" (toggles to "Remove" once added). Right-click a Movers/Scanner row → "Add {sym} to watchlist".

- [ ] **Step 4: Demo entry/exit.** From a live session with a few chart panels open, click "Try demo" / Practice → demo. Verify: the process relaunches into `-demo`; the watchlist auto-populates with the 12 synth symbols and auto-shows; open charts retarget to synth symbols (no "unknown symbol" toasts); link groups green/red/blue/yellow follow uni[0..3]. Return to live → the pre-demo workspace comes back exactly (panels opened in demo gone, panels closed in demo returned; the auto-added watchlist panel disappears if it wasn't open pre-demo). Confirm no doomed-EnsureSymbol toasts across either boundary. Then hard-refresh mid-demo and Return to live → symbols un-wedge to `US.AAPL` (no fictional symbols stuck), real DB not poisoned.

- [ ] **Step 5: Engine logs sanity.** Confirm the watchlist poller issues exactly one 3203 per tick with a non-empty list and zero with an empty list; no batch-failure spam.

---

## Verification summary

- **Engine unit:** `watchlist/list_test.go`, `watchlist/poller_test.go`, `uihub/commands_test.go` (watchlist cases), mirror coverage.
- **UI unit:** `WatchlistStore.test.ts`, `demoTransition.test.ts`, `reannounceGate.test.ts`, `DemandRegistry.test.ts` additions.
- **Integration/manual (Task 14):** panel CRUD + persistence + multi-window; chart/scanner menus; demo entry/revert (both snapshot and no-snapshot paths); reannounce quiet across boundaries.
- **Drift gate:** `make gen-ts-check` after any `payloads.go`/`wsmsg.go` change.

## Open question flagged for Earl

**Replay-mode watchlist.** The spec (§Poller, §Accepted tradeoffs) says the poller "runs against whatever the replay requester answers" and shows dash placeholders. In the actual code, `startPollers` is **not** called in the replay branch (main.go:701) and the replay feed exposes no 3203 requester — so this plan does not wire the watchlist poller into replay in v1 (the replay watchlist panel is empty). This is consistent with the spec's own "accepted v1 behavior / the data columns are a live/demo feature" framing but is a literal deviation. If Earl wants membership-with-dashes in replay, that's a small follow-up (start the poller in the replay branch against a no-op requester so binary-split yields dashes) — call it out before or after execution.
