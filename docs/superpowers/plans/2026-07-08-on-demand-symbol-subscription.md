# On-Demand Symbol Subscription Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the UI so any symbol typed into a chart/ladder/tape/news panel subscribes on the engine on demand (validated first), releases when the panel switches or closes, and never leaks quota on disconnect.

**Architecture:** Three uihub commands (`EnsureSymbol`/`ReleaseSymbol`, plus an upgraded `FocusGroup`) map 1:1 onto the engine's existing refcounted `feed.Ensure`/`Release` demand model. The hub owns per-connection demand state (released on disconnect via the existing `Unregister` teardown) and exposes it to the news poller. Existence is validated before ack via a new subscription-free `Qot_GetSecuritySnapshot` (3203) probe. On the UI side a `DemandRegistry` (owned by `App.tsx` alongside `LinkGroups`) drives per-panel ensure/release from `PanelFrame` and re-announces on reconnect.

**Tech Stack:** Go engine (`internal/feed`, `internal/feed/opend`, `internal/uihub`, `cmd/etape`); TypeScript/React UI (`ui/src/wire`, `ui/src/chrome`); moomoo OpenD protobuf/TCP; tygo for Go→TS type generation; Go `testing` + vitest.

## Global Constraints

- **Go module path:** `github.com/earlisreal/eTape/engine`. All engine imports use this prefix.
- **Commit messages:** body only. **Never** add a `Co-Authored-By:`/`Generated with`/AI-attribution trailer (user global instruction, overrides any harness default).
- **Hub single-writer discipline:** `Hub` has no mutex. Every field below the channel declarations is touched **only** inside `Run`'s goroutine; other goroutines communicate exclusively via channels. New cross-goroutine hub state MUST follow this pattern (channel-send mutation, channel-request/reply snapshot) — verified by `go test -race`. Do not add a `sync.Mutex` to `Hub`.
- **tygo drift gate:** `wsmsg` DTOs in `engine/internal/uihub/wsmsg/payloads.go` are tygo-generated into `ui/src/gen/wsmsg.ts`. After adding/changing any DTO, run `make gen-ts` in `engine/` and commit the regenerated `ui/src/gen/wsmsg.ts`. CI enforces no drift via `make gen-ts-check`.
- **Engine tests run with `-race`:** the repo's `make test` is `go test -race ./...`. All engine test commands below include `-race`.
- **Boot-order invariant:** the uihub server must be listening **before** the OpenD connection is dialed (`main.go` comment). The feed is therefore injected into the hub *after* construction via `SetFeed`, never as a constructor argument. Replay and tests never call `SetFeed`, so the hub's feed stays nil and every command self-degrades (probe skipped, ensure/release no-op, ack accepted).
- **Dynamic demand ids** are `dyn/<connID>/<demandId>` — disjoint from boot ids (`boot-watch-*`/`boot-focus-*`) by the `/` vs `-` separator. Never change the boot id scheme.

---

## Verified facts this plan relies on (do not re-derive)

- `subman.Ensure` upserts by `Demand.ID` and never rejects; `subman.Release` is a no-op for unknown ids. **No subman code changes** are needed beyond one new test (Task 6).
- `Qot_GetSecuritySnapshot` (3203) behavior, confirmed live 2026-07-08 against OpenD:
  - Valid symbol (`US.AAPL`) → `RetType == 0`, one snapshot row.
  - Invalid symbol (`US.ZZZZQQ`) → `RetType == -1`, `RetMsg == "Unknown stock. ZZZZQQ"` (NOT a clean empty list — it is a `-1` failure whose message contains `"Unknown stock"`).
  - Mixed valid+invalid in one batch → the **whole** request fails `-1 "Unknown stock…"`. Therefore the probe MUST be single-symbol.
  - Classification: transport/ctx error → feed-unavailable; `RetType != 0` with `RetMsg` containing (case-insensitive) `"unknown stock"` → unknown-symbol; any other `RetType != 0` or empty list → treat conservatively (unknown-symbol only for empty list; everything else → feed-unavailable).
- `ProtoQotGetSecuritySnapshot uint32 = 3203` is **already declared** in `engine/internal/feed/opend/protoid.go`, and the `qotgetsecuritysnapshot` protobuf package is **already generated** at `engine/internal/feed/opend/pb/qotgetsecuritysnapshot/`. No proto regeneration needed.
- HK entitlement is LV1: a `SubBook` on an HK symbol fails and retries forever in the subman. The `focused` profile adds `SubBook` **only** for `US.` symbols.
- `feed.SubType` constants: `SubQuote`, `SubBook`, `SubTicker`, `SubKL1m`. Helpers: `feed.WatchDemand(id, symbol)` = `{SubTicker, SubKL1m}`; `feed.FocusedDemand(id, symbol)` = `{SubQuote, SubBook, SubTicker, SubKL1m}, Focused: true` (unconditional book — do NOT use it for the profile mapping; build focused demands manually with the US-book guard).
- `PanelConfig.id` (`ui/src/chrome/workspace.ts`) is the stable per-panel-instance id → it is the `demandId`.
- `WsClient.onState(cb)` fires `cb` immediately with the current state, then on every transition; `WsClient` re-sends `subscribe` for live topics on reconnect but does **not** re-send commands — so `DemandRegistry` must re-announce demands itself on `state === "open"`.

---

## File Structure

**Engine — new files:**
- `engine/internal/feed/errors.go` — `ErrUnknownSymbol`, `ErrFeedUnavailable` sentinels (feed-level, so uihub classifies without importing `opend`).
- `engine/internal/feed/opend/snapshot.go` — the `Qot_GetSecuritySnapshot` (3203) probe helper `(*backfill).securityExists`.
- `engine/cmd/etape/news_symbols.go` — `newsSymbols(...)` composition helper (testable without a running hub).

**Engine — modified files:**
- `engine/internal/feed/opend/opendfeed.go` — add `OpenDFeed.Validate` + positive cache field.
- `engine/internal/uihub/api.go` — exported `Feed` interface; wire `newCommands`.
- `engine/internal/uihub/hub.go` — feed slot + `SetFeed`; demand state, channels, handlers, `EnsureDemand`/`ReleaseDemand`/`ActiveDemandSymbols`; augment register/unregister/drain.
- `engine/internal/uihub/commands.go` — `demandCtl` interface; `handle(ctx, name, args, connID)`; `EnsureSymbol`/`ReleaseSymbol`/`FocusGroup` handlers; `demandForProfile`/`supportedMarket` helpers.
- `engine/internal/uihub/conn.go` — `commandHandler.handle` signature; pass `ctx`+`c.nid` in `dispatch`.
- `engine/internal/uihub/wsmsg/payloads.go` — `EnsureSymbolArgs`, `ReleaseSymbolArgs`, `FocusGroupArgs`.
- `engine/internal/uihub/export_test.go` — `NewCommandsForTest` extra params.
- `engine/cmd/etape/main.go` — `hub.SetFeed(fd)`; `startPollers` signature + news closure.
- `ui/src/gen/wsmsg.ts` — regenerated (do not hand-edit).

**UI — new files:**
- `ui/src/wire/DemandRegistry.ts` + `ui/src/wire/DemandRegistry.test.ts`.

**UI — modified files:**
- `ui/src/chrome/panels/registry.tsx` — `PanelDef.demand`; chart/tape/ladder/news entries.
- `ui/src/App.tsx` — construct + pass `DemandRegistry`.
- `ui/src/chrome/AppShell.tsx` — prop passthrough into `PanelFrame`.
- `ui/src/chrome/PanelFrame.tsx` — ensure/release effects; pinned-commit ack gate.

---

## Task 1: Snapshot existence probe (3203) + feed-level error sentinels

**Files:**
- Create: `engine/internal/feed/errors.go`
- Create: `engine/internal/feed/opend/snapshot.go`
- Test: `engine/internal/feed/opend/snapshot_test.go`

**Interfaces:**
- Produces: `feed.ErrUnknownSymbol`, `feed.ErrFeedUnavailable` (package-level `error` sentinels); `(*backfill).securityExists(ctx context.Context, symbol string) error` — returns `nil` if the symbol exists, `feed.ErrUnknownSymbol` if OpenD reports it unknown (or an empty snapshot list), `feed.ErrFeedUnavailable` (wrapping the cause) on any transport/decode/other server error.
- Consumes: the existing `rpc` seam (`(*backfill).rpc.Request(ctx, protoID, req) (Frame, error)`), `parseSymbol`, `ProtoQotGetSecuritySnapshot`, and the generated `qotgetsecuritysnapshot` package.

- [ ] **Step 1: Write the failing tests**

Create `engine/internal/feed/opend/snapshot_test.go`. Follow the existing `backfill_test.go` fake-rpc conventions (a `fakeRPC` that returns a canned `Frame` or error). If `backfill_test.go` already defines a reusable fake rpc + a `mustMarshal` helper, reuse them (do not redefine); otherwise add a minimal local one as shown.

```go
package opend

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// snapRPC is a one-shot rpc seam returning a fixed frame/error for the probe.
type snapRPC struct {
	resp *qotgetsecuritysnapshot.Response
	err  error
	got  uint32 // protoID actually requested
}

func (s *snapRPC) Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error) {
	s.got = protoID
	if s.err != nil {
		return Frame{}, s.err
	}
	b, _ := proto.Marshal(s.resp)
	return Frame{ProtoID: protoID, Body: b}, nil
}

func snapshotResp(retType int32, retMsg string, rows int) *qotgetsecuritysnapshot.Response {
	list := make([]*qotgetsecuritysnapshot.Snapshot, rows)
	for i := range list {
		list[i] = &qotgetsecuritysnapshot.Snapshot{}
	}
	return &qotgetsecuritysnapshot.Response{
		RetType: proto.Int32(retType),
		RetMsg:  proto.String(retMsg),
		S2C:     &qotgetsecuritysnapshot.S2C{SnapshotList: list},
	}
}

func TestSecurityExists_Ok(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 1)}
	bf := newBackfill(r)
	if err := bf.securityExists(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if r.got != ProtoQotGetSecuritySnapshot {
		t.Fatalf("want protoID %d, got %d", ProtoQotGetSecuritySnapshot, r.got)
	}
}

func TestSecurityExists_UnknownStock(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "Unknown stock. ZZZZQQ", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.ZZZZQQ")
	if !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol, got %v", err)
	}
}

func TestSecurityExists_EmptyListIsUnknown(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.NADA")
	if !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol on empty list, got %v", err)
	}
}

func TestSecurityExists_TransportIsUnavailable(t *testing.T) {
	r := &snapRPC{err: ErrRequestTimeout}
	err := newBackfill(r).securityExists(context.Background(), "US.AAPL")
	if !errors.Is(err, feed.ErrFeedUnavailable) {
		t.Fatalf("want ErrFeedUnavailable, got %v", err)
	}
}

func TestSecurityExists_OtherRetTypeIsUnavailable(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "server busy", 0)}
	err := newBackfill(r).securityExists(context.Background(), "US.AAPL")
	if !errors.Is(err, feed.ErrFeedUnavailable) {
		t.Fatalf("want ErrFeedUnavailable for non-'unknown stock' failure, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/feed/opend/ -run TestSecurityExists`
Expected: FAIL to compile — `securityExists` undefined, `feed.ErrUnknownSymbol`/`feed.ErrFeedUnavailable` undefined.

- [ ] **Step 3: Add the sentinels**

Create `engine/internal/feed/errors.go`:

```go
package feed

import "errors"

// ErrUnknownSymbol means the market-data source reports no such symbol.
// Callers reject the load with a "unknown symbol" reason. Negative results
// are never cached (an intraday listing must not be locked out).
var ErrUnknownSymbol = errors.New("feed: unknown symbol")

// ErrFeedUnavailable means the source could not answer (transport error,
// timeout, decode failure, or an ambiguous server error). Callers reject the
// load with a "feed unavailable" reason and the user retries.
var ErrFeedUnavailable = errors.New("feed: unavailable")
```

- [ ] **Step 4: Implement the probe helper**

Create `engine/internal/feed/opend/snapshot.go`:

```go
package opend

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// securityExists probes a single symbol via Qot_GetSecuritySnapshot (3203) —
// subscription-free and quota-free. It returns nil if the symbol exists,
// feed.ErrUnknownSymbol if OpenD reports it unknown, or feed.ErrFeedUnavailable
// (wrapping the cause) on any other failure. Single-symbol only: a batch
// containing one bad code fails the whole request (verified live 2026-07-08).
func (b *backfill) securityExists(ctx context.Context, symbol string) error {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.ErrUnknownSymbol
	}
	req := &qotgetsecuritysnapshot.Request{C2S: &qotgetsecuritysnapshot.C2S{
		SecurityList: []*qotcommon.Security{sec},
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetSecuritySnapshot, req)
	if err != nil {
		return fmt.Errorf("%w: snapshot rpc: %v", feed.ErrFeedUnavailable, err)
	}
	var resp qotgetsecuritysnapshot.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("%w: snapshot decode: %v", feed.ErrFeedUnavailable, err)
	}
	if resp.GetRetType() != 0 {
		msg := resp.GetRetMsg()
		if strings.Contains(strings.ToLower(msg), "unknown stock") {
			return fmt.Errorf("%w: %s", feed.ErrUnknownSymbol, msg)
		}
		return fmt.Errorf("%w: %s", feed.ErrFeedUnavailable, msg)
	}
	if len(resp.GetS2C().GetSnapshotList()) == 0 {
		return feed.ErrUnknownSymbol
	}
	return nil
}
```

> If `qotgetsecuritysnapshot.Snapshot` is not the element type of `SnapshotList`, open `engine/internal/feed/opend/pb/qotgetsecuritysnapshot/Qot_GetSecuritySnapshot.pb.go` and use the actual `S2C.SnapshotList` element type in the test's `snapshotResp` helper. The production code above only calls `GetSnapshotList()` + `len`, so it is unaffected.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -run TestSecurityExists`
Expected: PASS (all 5 cases).

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/feed/errors.go internal/feed/opend/snapshot.go internal/feed/opend/snapshot_test.go
git commit -m "feat(feed): Qot_GetSecuritySnapshot existence probe + feed error sentinels"
```

---

## Task 2: `OpenDFeed.Validate` with positive cache

**Files:**
- Modify: `engine/internal/feed/opend/opendfeed.go`
- Test: `engine/internal/feed/opend/opendfeed_test.go` (add cases; create if absent)

**Interfaces:**
- Consumes: `(*backfill).securityExists` (Task 1).
- Produces: `func (f *OpenDFeed) Validate(ctx context.Context, symbol string) error` — nil / `feed.ErrUnknownSymbol` / `feed.ErrFeedUnavailable`. Applies a 2 s timeout and caches positive results for the process lifetime (negatives never cached). This makes `*OpenDFeed` satisfy `uihub.Feed` (Task 3).

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/feed/opend/opendfeed_test.go` (create the file with `package opend` if it does not exist). Reuse the `snapRPC` fake from Task 1 by constructing the `OpenDFeed`'s backfill through the exported constructor path. Because `NewOpenDFeed` takes a `*Client` (not the `rpc` seam), inject the fake by building the feed then swapping its backfill:

```go
func TestValidate_CachesPositive(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 1)}
	f := &OpenDFeed{bf: newBackfill(r), validated: map[string]struct{}{}}
	if err := f.Validate(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("first call: want nil, got %v", err)
	}
	r.err = ErrNotConnected // any later RPC would now fail…
	if err := f.Validate(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("cached call must not RPC: want nil, got %v", err)
	}
}

func TestValidate_UnknownNotCached(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "Unknown stock. X", 0)}
	f := &OpenDFeed{bf: newBackfill(r), validated: map[string]struct{}{}}
	if err := f.Validate(context.Background(), "US.X"); !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol, got %v", err)
	}
	// negative must not be cached — a now-valid symbol resolves.
	r.resp = snapshotResp(0, "", 1)
	if err := f.Validate(context.Background(), "US.X"); err != nil {
		t.Fatalf("second call after listing: want nil, got %v", err)
	}
}
```

Add `"errors"` and the `feed` import if missing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/feed/opend/ -run TestValidate`
Expected: FAIL — `OpenDFeed.validated` field and `Validate` method undefined.

- [ ] **Step 3: Add the cache field**

In `engine/internal/feed/opend/opendfeed.go`, add a field to the `OpenDFeed` struct (in the mutex-guarded block, next to `fetched`):

```go
	mu          sync.Mutex
	fetched     map[string]time.Time // history-quota dedup window (30 days)
	validated   map[string]struct{}  // process-lifetime positive existence cache
	decodeFails uint64
```

Initialize it in `NewOpenDFeed`'s returned struct literal (next to `fetched: make(map[string]time.Time),`):

```go
		fetched:   make(map[string]time.Time),
		validated: make(map[string]struct{}),
```

- [ ] **Step 4: Implement `Validate`**

Add (near `QuoteSnapshot`, before `var _ feed.Feed`):

```go
// Validate confirms a symbol exists before the UI commits a panel load. It is
// subscription-free and quota-free (Qot_GetSecuritySnapshot). Positive results
// are cached for the process lifetime; negatives are not (an intraday listing
// must not be locked out). Returns feed.ErrUnknownSymbol or
// feed.ErrFeedUnavailable on failure.
func (f *OpenDFeed) Validate(ctx context.Context, symbol string) error {
	f.mu.Lock()
	_, ok := f.validated[symbol]
	f.mu.Unlock()
	if ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := f.bf.securityExists(ctx, symbol); err != nil {
		return err
	}
	f.mu.Lock()
	f.validated[symbol] = struct{}{}
	f.mu.Unlock()
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -run 'TestValidate|TestSecurityExists'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/feed/opend/opendfeed.go internal/feed/opend/opendfeed_test.go
git commit -m "feat(feed): OpenDFeed.Validate existence check with positive cache"
```

---

## Task 3: Hub demand tracking + feed seam + `SetFeed`

**Files:**
- Modify: `engine/internal/uihub/api.go`
- Modify: `engine/internal/uihub/hub.go`
- Test: `engine/internal/uihub/hub_demand_test.go` (new)

**Interfaces:**
- Produces (exported): `uihub.Feed` interface (`Validate(ctx, symbol) error`, `Ensure(feed.Demand)`, `Release(id string)`); `func (h *Hub) SetFeed(f Feed)`; `func (h *Hub) EnsureDemand(connID uint64, d feed.Demand)`; `func (h *Hub) ReleaseDemand(connID uint64, demandID string)`; `func (h *Hub) ActiveDemandSymbols() []string` (deduped, sorted, includes `interest` demands).
- Produces (unexported, for Task 5): `func (h *Hub) feed() Feed` (nil-safe getter, passed to `newCommands`).
- Consumes: the existing `client.id()`, the run-loop select/drain, `handleRegister`/`handleUnregister`.

- [ ] **Step 1: Write the failing test**

Create `engine/internal/uihub/hub_demand_test.go` (`package uihub`). **Reuse the existing `fakeClient` from `hub_test.go`** — it already implements the `client` interface via an `nid uint64` field, so `&fakeClient{nid: 7}` works. Do NOT redeclare `type fakeClient` (verified: `hub_test.go:17` already defines it — a redeclaration is a duplicate-symbol compile error). Add only `spyHubFeed` + `runHub` below (verified: neither name exists elsewhere in the package).

```go
package uihub

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

type spyHubFeed struct {
	mu       sync.Mutex
	ensured  []feed.Demand
	released []string
}

func (s *spyHubFeed) Validate(context.Context, string) error { return nil }
func (s *spyHubFeed) Ensure(d feed.Demand) {
	s.mu.Lock()
	s.ensured = append(s.ensured, d)
	s.mu.Unlock()
}
func (s *spyHubFeed) Release(id string) {
	s.mu.Lock()
	s.released = append(s.released, id)
	s.mu.Unlock()
}

func runHub(t *testing.T) (*Hub, func()) {
	t.Helper()
	h, _ := NewHubForTest(clock.System{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	return h, cancel
}

func TestHubDemand_TrackReleaseSnapshot(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 7}
	h.Register(c)
	h.EnsureDemand(7, feed.WatchDemand("dyn/7/p1", "US.AAPL"))
	h.EnsureDemand(7, feed.Demand{ID: "dyn/7/p2", Symbol: "US.MSFT"}) // interest (no subs)
	h.sync()

	got := h.ActiveDemandSymbols()
	want := []string{"US.AAPL", "US.MSFT"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveDemandSymbols = %v, want %v", got, want)
	}
	if len(sf.ensured) != 2 {
		t.Fatalf("feed.Ensure calls = %d, want 2", len(sf.ensured))
	}

	h.ReleaseDemand(7, "dyn/7/p1")
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 1 || got[0] != "US.MSFT" {
		t.Fatalf("after release = %v, want [US.MSFT]", got)
	}
}

func TestHubDemand_UnregisterReleasesAll(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 3}
	h.Register(c)
	h.EnsureDemand(3, feed.WatchDemand("dyn/3/a", "US.AAPL"))
	h.EnsureDemand(3, feed.WatchDemand("dyn/3/b", "US.NVDA"))
	h.sync()

	h.Unregister(c)
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 0 {
		t.Fatalf("after unregister = %v, want empty", got)
	}
	sf.mu.Lock()
	rel := append([]string(nil), sf.released...)
	sf.mu.Unlock()
	sort.Strings(rel)
	if !reflect.DeepEqual(rel, []string{"dyn/3/a", "dyn/3/b"}) {
		t.Fatalf("released = %v, want both ids", rel)
	}
}

func TestHubDemand_EnsureAfterUnregisterDropped(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 9}
	h.Register(c)
	h.Unregister(c)
	h.sync()
	// A late ensure for a gone conn must NOT re-create state or subscribe.
	h.EnsureDemand(9, feed.WatchDemand("dyn/9/x", "US.AAPL"))
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 0 {
		t.Fatalf("late ensure leaked: %v", got)
	}
	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 0 {
		t.Fatalf("feed.Ensure called for dead conn: %d", n)
	}
}

func TestHubDemand_NilFeedNoPanic(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 1}
	h.Register(c)
	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 1 {
		t.Fatalf("nil-feed should still track demands: %v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run TestHubDemand`
Expected: FAIL to compile — `SetFeed`, `EnsureDemand`, `ReleaseDemand`, `ActiveDemandSymbols`, `Feed` undefined.

- [ ] **Step 3: Add the exported `Feed` interface**

In `engine/internal/uihub/api.go`, add `"context"` and the feed import, then add the interface next to `Indicators`:

```go
import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// Feed is the market-data control surface uihub needs for on-demand symbol
// subscription (satisfied by *opend.OpenDFeed). It is injected after
// construction via Hub.SetFeed because the OpenD feed is created only after
// the hub is already listening; replay/tests leave it nil.
type Feed interface {
	Validate(ctx context.Context, symbol string) error
	Ensure(d feed.Demand)
	Release(id string)
}
```

Change the `newCommands` call inside `New` to pass the hub as demand controller and its feed getter:

```go
	cmd := newCommands(ex, st, ind, h, h.feed)
```

- [ ] **Step 4: Add hub state, channels, feed slot, and public methods**

In `engine/internal/uihub/hub.go`, extend imports:

```go
import (
	"context"
	"encoding/json"
	"sort"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)
```

Add request types and a feed box near the other small types (after `pub`):

```go
type ensureDemandReq struct {
	connID uint64
	d      feed.Demand
}

type releaseDemandReq struct {
	connID   uint64
	demandID string
}

// feedBox lets the (single-write, many-read) feed reference live in an
// atomic.Pointer so SetFeed (called once at boot from main's goroutine) races
// safely with Validate reads in conn goroutines and Ensure/Release in Run.
type feedBox struct{ f Feed }
```

Add channels + run-loop-owned demand maps + the feed slot to the `Hub` struct:

```go
	register        chan client
	unregister      chan client
	subCh           chan subReq
	unsubCh         chan subReq
	ensureDemandCh  chan ensureDemandReq
	releaseDemandCh chan releaseDemandReq
	demandSnapCh    chan chan []string
	mdCh            chan md.Update
	execCh          chan exec.Update
	pubCh           chan pub
	syncCh          chan chan struct{} // test barrier
	closed          chan struct{}      // closed when Run returns; unblocks stuck senders

	feedSlot atomic.Pointer[feedBox]

	// Run-loop-owned:
	clients    map[client]map[wsmsg.Topic]bool
	demands    map[uint64]map[string]string // connID -> demandID -> symbol
	demandLive map[uint64]bool              // connID currently registered
	pendKeep   map[string]staged
	tapePend   map[string][]wsmsg.Tick
	acctPend   map[string]staged
	posLatest  staged
	posDirty   bool
```

Initialize the new channels/maps in `NewHub`'s returned literal:

```go
		ensureDemandCh:  make(chan ensureDemandReq),
		releaseDemandCh: make(chan releaseDemandReq),
		demandSnapCh:    make(chan chan []string),
```
```go
		clients:    map[client]map[wsmsg.Topic]bool{},
		demands:    map[uint64]map[string]string{},
		demandLive: map[uint64]bool{},
```

Add the public entry points (near `Subscribe`/`Unsubscribe`):

```go
// SetFeed injects the market-data control surface after the hub is running.
// Safe to call once from boot; nil until then (replay/tests never call it).
func (h *Hub) SetFeed(f Feed) { h.feedSlot.Store(&feedBox{f: f}) }

func (h *Hub) feed() Feed {
	if b := h.feedSlot.Load(); b != nil {
		return b.f
	}
	return nil
}

// EnsureDemand records a connection's demand and subscribes it (Run-loop side).
func (h *Hub) EnsureDemand(connID uint64, d feed.Demand) {
	select {
	case h.ensureDemandCh <- ensureDemandReq{connID: connID, d: d}:
	case <-h.closed:
	}
}

// ReleaseDemand forgets a connection's demand and unsubscribes it.
func (h *Hub) ReleaseDemand(connID uint64, demandID string) {
	select {
	case h.releaseDemandCh <- releaseDemandReq{connID: connID, demandID: demandID}:
	case <-h.closed:
	}
}

// ActiveDemandSymbols snapshots the deduped, sorted set of symbols under live
// demand across all connections (including interest demands with no subs).
// Used by the news poller to compose its rotation set.
func (h *Hub) ActiveDemandSymbols() []string {
	reply := make(chan []string, 1)
	select {
	case h.demandSnapCh <- reply:
	case <-h.closed:
		return nil
	}
	select {
	case out := <-reply:
		return out
	case <-h.closed:
		return nil
	}
}
```

Add the three cases to `Run`'s select (after the `unsubCh` case):

```go
		case r := <-h.ensureDemandCh:
			h.handleEnsureDemand(r)
		case r := <-h.releaseDemandCh:
			h.handleReleaseDemand(r)
		case reply := <-h.demandSnapCh:
			h.handleDemandSnapshot(reply)
```

Add ensure/release (not the snapshot — it produces a reply, not inbound work) to `drain`'s select so `sync()` flushes them before the barrier closes:

```go
		case r := <-h.ensureDemandCh:
			h.handleEnsureDemand(r)
		case r := <-h.releaseDemandCh:
			h.handleReleaseDemand(r)
```

Augment `handleRegister` / `handleUnregister`:

```go
func (h *Hub) handleRegister(c client) {
	h.clients[c] = map[wsmsg.Topic]bool{}
	h.demandLive[c.id()] = true
}

func (h *Hub) handleUnregister(c client) {
	id := c.id()
	if m := h.demands[id]; m != nil {
		if f := h.feed(); f != nil {
			for did := range m {
				f.Release(did)
			}
		}
		delete(h.demands, id)
	}
	delete(h.demandLive, id)
	delete(h.clients, c)
	c.close()
}
```

Add the demand handlers (near `handleSub`/`handleUnsub`):

```go
func (h *Hub) handleEnsureDemand(r ensureDemandReq) {
	if !h.demandLive[r.connID] {
		return // conn already gone; drop so it can never leak quota
	}
	m := h.demands[r.connID]
	if m == nil {
		m = map[string]string{}
		h.demands[r.connID] = m
	}
	m[r.d.ID] = r.d.Symbol
	if f := h.feed(); f != nil {
		f.Ensure(r.d)
	}
}

func (h *Hub) handleReleaseDemand(r releaseDemandReq) {
	if m := h.demands[r.connID]; m != nil {
		delete(m, r.demandID)
	}
	if f := h.feed(); f != nil {
		f.Release(r.demandID)
	}
}

func (h *Hub) handleDemandSnapshot(reply chan []string) {
	set := map[string]struct{}{}
	for _, m := range h.demands {
		for _, sym := range m {
			set[sym] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	reply <- out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd engine && go test -race ./internal/uihub/ -run TestHubDemand`
Expected: PASS (all 4 cases).

- [ ] **Step 6: Verify the whole uihub package still compiles/tests (New's newCommands call will fail until Task 5)**

Run: `cd engine && go build ./internal/uihub/`
Expected: FAIL — `newCommands` now called with 5 args in `api.go` but still defined with 3. This is expected; Task 5 completes the signature. **Do not commit Task 3 alone** — it leaves the package uncompilable. Proceed to Task 5 before committing, OR temporarily leave `api.go`'s `newCommands` call as `newCommands(ex, st, ind)` and only switch it in Task 5. **Chosen approach:** keep the `api.go` change (`newCommands(ex, st, ind, h, h.feed)`) in this task and defer the commit until Task 5 (they are one logical unit — the hub is the demand controller commands calls into). Mark this task's checkbox complete once Step 5 passes; commit at the end of Task 5.

> Rationale for the deferred commit: Tasks 3 and 5 straddle one interface (`demandCtl` implemented by `Hub`, consumed by `commands`). Splitting them cleanly at a green build is not possible without a throwaway shim. The reviewer gate is still meaningful — review Task 3's hub tests (passing in isolation via Step 5) before starting Task 5.

---

## Task 4: `wsmsg` command-arg DTOs + regenerate TS

**Files:**
- Modify: `engine/internal/uihub/wsmsg/payloads.go`
- Regenerate: `ui/src/gen/wsmsg.ts`

**Interfaces:**
- Produces: `wsmsg.EnsureSymbolArgs{DemandID, Symbol, Profile string}`, `wsmsg.ReleaseSymbolArgs{DemandID string}`, `wsmsg.FocusGroupArgs{Group, Symbol string}` and their generated TS interfaces.

- [ ] **Step 1: Add the DTOs**

In `engine/internal/uihub/wsmsg/payloads.go`, next to the other `*Args` structs (e.g. after `SetConfigArgs`):

```go
// EnsureSymbolArgs subscribes a panel's symbol on demand. profile is one of
// "watch" | "focused" | "interest". demandId is the UI panel instance id.
type EnsureSymbolArgs struct {
	DemandID string `json:"demandId"`
	Symbol   string `json:"symbol"`
	Profile  string `json:"profile"`
}

// ReleaseSymbolArgs drops a panel's on-demand subscription.
type ReleaseSymbolArgs struct {
	DemandID string `json:"demandId"`
}

// FocusGroupArgs carries a link-group focus change for engine-side existence
// validation (the demand itself arrives from the member panels).
type FocusGroupArgs struct {
	Group  string `json:"group"`
	Symbol string `json:"symbol"`
}
```

- [ ] **Step 2: Regenerate the TS contract**

Run: `cd engine && make gen-ts`
Then verify no unexpected drift and the new interfaces are present:

Run: `cd engine && make gen-ts-check`
Expected: exit 0 (no drift after regeneration).

Run: `grep -n "EnsureSymbolArgs\|ReleaseSymbolArgs\|FocusGroupArgs" ../ui/src/gen/wsmsg.ts`
Expected: three `export interface …Args` matches.

- [ ] **Step 3: Verify engine still builds**

Run: `cd engine && go build ./internal/uihub/wsmsg/`
Expected: success.

- [ ] **Step 4: Commit**

```bash
cd /Users/earl.savadera/Projects/eTape
git add engine/internal/uihub/wsmsg/payloads.go ui/src/gen/wsmsg.ts
git commit -m "feat(wsmsg): EnsureSymbol/ReleaseSymbol/FocusGroup command args"
```

---

## Task 5: uihub command handlers (`EnsureSymbol`/`ReleaseSymbol`/`FocusGroup`)

**Files:**
- Modify: `engine/internal/uihub/commands.go`
- Modify: `engine/internal/uihub/conn.go`
- Modify: `engine/internal/uihub/export_test.go`
- Test: `engine/internal/uihub/commands_test.go` (add cases + spies; update the 7 existing `newCommands(...)` and 9 existing `handle(...)` call sites)
- Test: `engine/internal/uihub/server_test.go` (add a `noopDemand` type; update the 5 existing `NewCommandsForTest(...)` call sites)

> **Call-site inventory** (verified — these break the moment the signatures change, so they are part of this task, not optional):
> - `commands_test.go` (`package uihub`): **7** `newCommands(ex, cfg, ind)` calls (lines ~41, 60, 69, 79, 88, 105, 117) → each gains `, &spyDemandCtl{}, func() Feed { return nil }`. **9** `.handle(name, args)` calls (lines ~42, 61, 70, 80, 89, 97, 106, 110, 118) → each becomes `.handle(context.Background(), name, args, 0)`.
> - `server_test.go` (`package uihub_test`): **5** `uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{})` calls (lines ~49, 123, 205, 284, 415) → add a local `type noopDemand struct{}` with no-op `EnsureDemand(uint64, feed.Demand)` / `ReleaseDemand(uint64, string)` methods, then pass `, noopDemand{}, func() uihub.Feed { return nil }` to each. (`demandCtl` is unexported, but `NewCommandsForTest`'s new param is typed `demandCtl`; an external package can still pass a value that structurally satisfies it — `noopDemand` just needs the two exported methods.)

**Interfaces:**
- Consumes: `wsmsg.EnsureSymbolArgs`/`ReleaseSymbolArgs`/`FocusGroupArgs` (Task 4); `uihub.Feed` + `Hub.EnsureDemand`/`ReleaseDemand` (Task 3); `feed.ErrUnknownSymbol` (Task 1); `feed.SubType` constants + `feed.WatchDemand`.
- Produces: `commandHandler.handle(ctx context.Context, name string, args json.RawMessage, connID uint64) wsmsg.AckMsg`; the `EnsureSymbol`/`ReleaseSymbol` command semantics and the upgraded `FocusGroup`.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/uihub/commands_test.go` (package `uihub`). Add spies + cases. **Also** apply the full call-site inventory above: update the 7 existing `newCommands(...)` calls and the 9 existing `.handle(...)` calls in this file, and update `server_test.go`'s 5 `NewCommandsForTest(...)` calls. Do this in Step 4/5 (the compile won't pass until all sites match the new signatures) — the new tests below are what you assert against once it compiles.

```go
type spyCmdFeed struct{ err error }

func (s *spyCmdFeed) Validate(context.Context, string) error { return s.err }
func (s *spyCmdFeed) Ensure(feed.Demand)                     {}
func (s *spyCmdFeed) Release(string)                         {}

type spyDemandCtl struct {
	ensured  []struct {
		conn uint64
		d    feed.Demand
	}
	released []struct {
		conn uint64
		id   string
	}
}

func (s *spyDemandCtl) EnsureDemand(conn uint64, d feed.Demand) {
	s.ensured = append(s.ensured, struct {
		conn uint64
		d    feed.Demand
	}{conn, d})
}
func (s *spyDemandCtl) ReleaseDemand(conn uint64, id string) {
	s.released = append(s.released, struct {
		conn uint64
		id   string
	}{conn, id})
}

func newCmdWith(t *testing.T, feedErr error, feedNil bool) (*commands, *spyDemandCtl, *spyCmdFeed) {
	t.Helper()
	dem := &spyDemandCtl{}
	sf := &spyCmdFeed{err: feedErr}
	getter := func() Feed { return sf }
	if feedNil {
		getter = func() Feed { return nil }
	}
	return newCommands(nil, nil, nil, dem, getter), dem, sf
}

func TestEnsureSymbol_AcceptsAndMapsWatch(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p1","symbol":"US.AAPL","profile":"watch"}`), 7)
	if ack.Status != "accepted" {
		t.Fatalf("status = %q reason=%q", ack.Status, ack.Reason)
	}
	if len(dem.ensured) != 1 {
		t.Fatalf("EnsureDemand calls = %d", len(dem.ensured))
	}
	got := dem.ensured[0]
	if got.conn != 7 || got.d.ID != "dyn/7/p1" || got.d.Symbol != "US.AAPL" {
		t.Fatalf("demand = %+v", got)
	}
	if got.d.Focused {
		t.Fatalf("watch must not be focused")
	}
	if !reflect.DeepEqual(got.d.Subs, []feed.SubType{feed.SubTicker, feed.SubKL1m}) {
		t.Fatalf("watch subs = %v", got.d.Subs)
	}
}

func TestEnsureSymbol_FocusedUSHasBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p2","symbol":"US.NVDA","profile":"focused"}`), 1)
	d := dem.ensured[0].d
	if !d.Focused {
		t.Fatal("focused flag missing")
	}
	if !reflect.DeepEqual(d.Subs, []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m, feed.SubBook}) {
		t.Fatalf("US focused subs = %v (want quote,ticker,kl1m,book)", d.Subs)
	}
}

func TestEnsureSymbol_FocusedHKNoBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p3","symbol":"HK.00700","profile":"focused"}`), 1)
	d := dem.ensured[0].d
	for _, s := range d.Subs {
		if s == feed.SubBook {
			t.Fatal("HK focused must NOT include SubBook (LV1 entitlement)")
		}
	}
}

func TestEnsureSymbol_InterestNoSubs(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p4","symbol":"US.T","profile":"interest"}`), 1)
	if len(dem.ensured[0].d.Subs) != 0 {
		t.Fatalf("interest must have no subs, got %v", dem.ensured[0].d.Subs)
	}
}

func TestEnsureSymbol_RejectsBadMarket(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p5","symbol":"XX.FOO","profile":"watch"}`), 1)
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("want blocked+no-ensure, got %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestEnsureSymbol_UnknownSymbolReverts(t *testing.T) {
	cd, dem, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p6","symbol":"US.ZZZZQQ","profile":"watch"}`), 1)
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("unknown symbol must block and not ensure: %q ensured=%d", ack.Status, len(dem.ensured))
	}
	if ack.Reason == "" {
		t.Fatal("expected a reason mentioning the symbol")
	}
}

func TestEnsureSymbol_FeedUnavailableBlocks(t *testing.T) {
	cd, _, _ := newCmdWith(t, feed.ErrFeedUnavailable, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p7","symbol":"US.AAPL","profile":"watch"}`), 1)
	if ack.Status != "blocked" {
		t.Fatalf("want blocked, got %q", ack.Status)
	}
}

func TestEnsureSymbol_NilFeedAcceptsNoProbe(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, true) // feed getter returns nil (replay)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p8","symbol":"US.AAPL","profile":"watch"}`), 1)
	if ack.Status != "accepted" || len(dem.ensured) != 1 {
		t.Fatalf("replay must accept and still track: %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestReleaseSymbol_NamespacedAlwaysAccepted(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "ReleaseSymbol", []byte(`{"demandId":"p1"}`), 7)
	if ack.Status != "accepted" {
		t.Fatalf("release status = %q", ack.Status)
	}
	if len(dem.released) != 1 || dem.released[0].conn != 7 || dem.released[0].id != "dyn/7/p1" {
		t.Fatalf("release = %+v", dem.released)
	}
}

func TestFocusGroup_ProbesAndAcks(t *testing.T) {
	cd, _, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.AAPL"}`), 1)
	if ack.Status != "accepted" {
		t.Fatalf("focus ack = %q", ack.Status)
	}
	cd2, _, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	if ack := cd2.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.ZZZZQQ"}`), 1); ack.Status != "blocked" {
		t.Fatalf("bad focus symbol must block, got %q", ack.Status)
	}
}
```

Ensure the test file imports `"context"`, `"reflect"`, and `"github.com/earlisreal/eTape/engine/internal/feed"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd engine && go test ./internal/uihub/ -run 'TestEnsureSymbol|TestReleaseSymbol|TestFocusGroup'`
Expected: FAIL to compile (`newCommands` arity, `handle` signature, `Feed` getter type).

- [ ] **Step 3: Update the `commandHandler` interface and `conn.dispatch`**

In `engine/internal/uihub/conn.go`, change the interface:

```go
type commandHandler interface {
	handle(ctx context.Context, name string, args json.RawMessage, connID uint64) wsmsg.AckMsg
}
```

And the `command` case in `dispatch` (pass `ctx` and this conn's id):

```go
	case "command":
		ack := c.cmd.handle(ctx, head.Name, head.Args, c.nid)
		ack.Kind = "ack"
		ack.CorrID = head.CorrID
		c.enqueueJSON(ack)
```

Remove the now-unnecessary `_ = ctx` line at the end of `dispatch` (ctx is used).

- [ ] **Step 4: Update `commands` struct, `newCommands`, and add handlers**

In `engine/internal/uihub/commands.go`, extend imports (`context`, `errors`, `fmt`, `strings`, `feed`):

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)
```

Add the `demandCtl` interface, extend the struct + constructor:

```go
// demandCtl is the hub surface the on-demand-subscription commands drive
// (satisfied by *Hub). EnsureDemand/ReleaseDemand are Run-loop-side; the
// blocking existence probe is done here in the conn goroutine via the feed
// getter before recording, so it never stalls the hub loop.
type demandCtl interface {
	EnsureDemand(connID uint64, d feed.Demand)
	ReleaseDemand(connID uint64, demandID string)
}

type commands struct {
	ex   execDoer
	cfg  configStore
	ind  indicatorCtl
	dem  demandCtl
	feed func() Feed
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl, dem demandCtl, feed func() Feed) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind, dem: dem, feed: feed}
}
```

Change the `handle` signature and replace the `FocusGroup` case; add the two new cases (place them right before `case "FocusGroup":`):

```go
func (cd *commands) handle(ctx context.Context, name string, args json.RawMessage, connID uint64) wsmsg.AckMsg {
	switch name {
	// … existing cases unchanged …
	case "EnsureSymbol":
		var a wsmsg.EnsureSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args")
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market")
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason)
		}
		d, ok := demandForProfile(fmt.Sprintf("dyn/%d/%s", connID, a.DemandID), a.Symbol, a.Profile)
		if !ok {
			return blocked("bad profile")
		}
		cd.dem.EnsureDemand(connID, d)
		return wsmsg.AckMsg{Status: "accepted"}
	case "ReleaseSymbol":
		var a wsmsg.ReleaseSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args")
		}
		cd.dem.ReleaseDemand(connID, fmt.Sprintf("dyn/%d/%s", connID, a.DemandID))
		return wsmsg.AckMsg{Status: "accepted"}
	case "FocusGroup":
		var a wsmsg.FocusGroupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market")
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason)
		}
		// Registers no demand — demands arrive from member panels as they follow.
		return wsmsg.AckMsg{Status: "accepted"}
	default:
		return blocked("unknown command: " + name)
	}
}
```

Add the helpers at the end of the file:

```go
// probe validates a symbol exists; returns "" to accept, else a block reason.
// Skipped when the feed is nil (replay/tests) so those paths accept.
func (cd *commands) probe(ctx context.Context, symbol string) string {
	f := cd.feed()
	if f == nil {
		return ""
	}
	err := f.Validate(ctx, symbol)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, feed.ErrUnknownSymbol):
		return "unknown symbol " + symbol
	default:
		return "feed unavailable"
	}
}

func supportedMarket(sym string) bool {
	return strings.HasPrefix(sym, "US.") || strings.HasPrefix(sym, "HK.")
}

// demandForProfile builds the feed.Demand for a profile. focused adds SubBook
// only for US symbols (HK is LV1: a book sub retries forever). Returns ok=false
// for an unknown profile.
func demandForProfile(id, symbol, profile string) (feed.Demand, bool) {
	switch profile {
	case "watch":
		return feed.WatchDemand(id, symbol), true
	case "focused":
		subs := []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m}
		if strings.HasPrefix(symbol, "US.") {
			subs = append(subs, feed.SubBook)
		}
		return feed.Demand{ID: id, Symbol: symbol, Subs: subs, Focused: true}, true
	case "interest":
		return feed.Demand{ID: id, Symbol: symbol}, true
	default:
		return feed.Demand{}, false
	}
}
```

- [ ] **Step 5: Update `export_test.go`**

```go
// NewCommandsForTest exposes newCommands to external test packages.
func NewCommandsForTest(ex execDoer, c configStore, i indicatorCtl, d demandCtl, f func() Feed) commandHandler {
	return newCommands(ex, c, i, d, f)
}
```

Then fix `server_test.go`'s 5 call sites (verified present). Add near its other local noop types:

```go
type noopDemand struct{}

func (noopDemand) EnsureDemand(uint64, feed.Demand) {}
func (noopDemand) ReleaseDemand(uint64, string)     {}
```

(add `"github.com/earlisreal/eTape/engine/internal/feed"` to `server_test.go`'s imports), and change each `uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{})` (and the `sc`-variant) to append `, noopDemand{}, func() uihub.Feed { return nil }`. Confirm no other callers: `grep -rn "NewCommandsForTest" engine/`.

- [ ] **Step 6: Run the full uihub test suite (Tasks 3 + 5 together)**

Run: `cd engine && go test -race ./internal/uihub/...`
Expected: PASS (hub demand tests, command tests, existing server/mirror tests all green).

- [ ] **Step 7: Verify the whole engine still builds (main.go's newCommands is internal; main.go SetFeed comes in Task 7)**

Run: `cd engine && go build ./...`
Expected: success (nothing in `cmd/etape` references the new hub methods yet).

- [ ] **Step 8: Commit (Tasks 3 + 5 as one unit)**

```bash
cd engine && git add internal/uihub/
git commit -m "feat(uihub): on-demand EnsureSymbol/ReleaseSymbol + FocusGroup probe, hub demand tracking"
```

---

## Task 6: subman empty-subs zero-quota test

**Files:**
- Test: `engine/internal/feed/opend/subman_test.go` (add one case)

**Interfaces:**
- Consumes: existing `subManager.Ensure` / `desired`. No production change.

- [ ] **Step 1: Write the test**

Add to `engine/internal/feed/opend/subman_test.go`, reusing the file's existing `newTestManager(t, budget) (*subManager, *fakeRPC, *clock.Fake)` helper (verified present at `subman_test.go:53`). `desired(capSlots int) (map[subKey]bool, []string)` is verified; `Ensure` + `desired` touch no rpc, so no `Qot_Sub` is issued.

```go
func TestEnsureEmptySubsUsesNoQuota(t *testing.T) {
	m, _, _ := newTestManager(t, 100)
	m.Ensure(feed.Demand{ID: "dyn/1/interest", Symbol: "US.AAPL"}) // no Subs
	want, _ := m.desired(100)
	if len(want) != 0 {
		t.Fatalf("empty-subs demand consumed %d slot(s), want 0", len(want))
	}
}
```

- [ ] **Step 2: Run and verify PASS**

Run: `cd engine && go test -race ./internal/feed/opend/ -run TestEnsureEmptySubsUsesNoQuota`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd engine && git add internal/feed/opend/subman_test.go
git commit -m "test(feed): empty-subs demand consumes zero quota"
```

---

## Task 7: main.go wiring — `SetFeed` + news symbol closure

**Files:**
- Create: `engine/cmd/etape/news_symbols.go`
- Modify: `engine/cmd/etape/main.go`
- Test: `engine/cmd/etape/news_symbols_test.go` (new)

**Interfaces:**
- Consumes: `hub.SetFeed`, `hub.ActiveDemandSymbols` (Task 3); `*opend.OpenDFeed` (satisfies `uihub.Feed` after Task 2).
- Produces: `newsSymbols(watchlist, watchCSV, focusCSV []string, demand func() []string) []string` (deduped, sorted union); `startPollers` gains `watchCSV, focusCSV []string` params.

- [ ] **Step 1: Write the failing test**

Create `engine/cmd/etape/news_symbols_test.go` (`package main`):

```go
package main

import (
	"reflect"
	"testing"
)

func TestNewsSymbols_UnionDedupSorted(t *testing.T) {
	got := newsSymbols(
		[]string{"US.AAPL", "US.MSFT"},         // config watchlist
		[]string{"US.MSFT", "US.NVDA"},         // --watch
		[]string{"US.F"},                        // --focus
		func() []string { return []string{"US.NVDA", "US.TSLA", ""} }, // live demands (+ empty)
	)
	want := []string{"US.AAPL", "US.F", "US.MSFT", "US.NVDA", "US.TSLA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewsSymbols_NilDemand(t *testing.T) {
	got := newsSymbols([]string{"US.AAPL"}, nil, nil, nil)
	if !reflect.DeepEqual(got, []string{"US.AAPL"}) {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./cmd/etape/ -run TestNewsSymbols`
Expected: FAIL — `newsSymbols` undefined.

- [ ] **Step 3: Implement `newsSymbols`**

Create `engine/cmd/etape/news_symbols.go`:

```go
package main

import "sort"

// newsSymbols composes the news poller's rotation set: config watchlist ∪ CLI
// --watch/--focus ∪ live UI demands (interest demands included), deduped and
// sorted. demand may be nil (no hub). Empty strings are dropped.
func newsSymbols(watchlist, watchCSV, focusCSV []string, demand func() []string) []string {
	set := map[string]struct{}{}
	add := func(ss []string) {
		for _, s := range ss {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	add(watchlist)
	add(watchCSV)
	add(focusCSV)
	if demand != nil {
		add(demand())
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Wire `SetFeed` and the news closure in `main.go`**

In `engine/cmd/etape/main.go`, inside the `if live { … }` block, immediately after `fd := opend.NewOpenDFeed(client, …)`:

```go
	fd := opend.NewOpenDFeed(client, opend.FeedOptions{
		Budget: cfg.Feed.QuotaSlots, Hysteresis: time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
		DisableExtendedTime: !cfg.Feed.ExtendedTime,
	})
	hub.SetFeed(fd) // enables on-demand EnsureSymbol/ReleaseSymbol + FocusGroup probe
```

Change the `startPollers` call site (still in the `if live` block) to pass the CLI symbol slices:

```go
	startPollers(ctx, cfg, client, hub, uihubClk, st, hasTZVenue(cfg), splitCSV(*watch), splitCSV(*focus))
```

Update the `startPollers` signature and its `symbols` closure:

```go
func startPollers(ctx context.Context, cfg config.Config, client *opend.Client, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, watchCSV, focusCSV []string) {
	symbols := func() []string {
		return newsSymbols(cfg.Feed.Watchlist, watchCSV, focusCSV, hub.ActiveDemandSymbols)
	}
	go func() { _ = scan.New(cfg.Scan, client, hub, clk).Run(ctx) }()
	go func() { _ = news.New(cfg.News, client, hub, clk, symbols).Run(ctx) }()
	go func() { _ = health.New(cfg.Health, hub, clk, moomooProbe{c: client}, nil, hasTZ).Run(ctx) }()
	_ = st
}
```

> `hub.ActiveDemandSymbols` is passed as a method value (`func() []string`). The poller already calls `symbols()` fresh every tick, so the set is live with no further change.

- [ ] **Step 5: Run tests + full build**

Run: `cd engine && go test -race ./cmd/etape/ -run TestNewsSymbols`
Expected: PASS.

Run: `cd engine && go build ./... && go test -race ./...`
Expected: build succeeds; full engine suite passes.

- [ ] **Step 6: Commit**

```bash
cd engine && git add cmd/etape/main.go cmd/etape/news_symbols.go cmd/etape/news_symbols_test.go
git commit -m "feat(engine): inject feed into hub; news poller follows live demands"
```

---

## Task 8: UI panel-def demand profiles

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx`
- Test: `ui/src/chrome/panels/registry.test.tsx` (verified: this file already exists and imports `{ PANELS, CATALOG, isDevPanel }` — add a new `describe` block, do not recreate the file or re-import `PANELS`)

**Interfaces:**
- Produces: `PanelDef.demand?: DemandProfile` where `DemandProfile = "watch" | "focused" | "interest"` (imported from `../../wire/DemandRegistry`, defined in Task 9 — write Task 9's type export first if executing strictly in order, or inline the union here and have DemandRegistry import it; **chosen: define `DemandProfile` in `ui/src/wire/DemandRegistry.ts` (Task 9) and import it here**). chart→`watch`, tape→`watch`, ladder→`focused`, news→`interest`.

> **Ordering note:** this task imports a type from `DemandRegistry.ts` (Task 9). Execute Task 9 before Task 8, or create the `DemandProfile` type export as the first step here. The steps below assume Task 9's `DemandProfile` export exists.

- [ ] **Step 1: Write the failing test**

Add a new `describe` block to the existing `ui/src/chrome/panels/registry.test.tsx` (`PANELS` is already imported there — do not re-import):

```ts
describe("panel demand profiles", () => {
  it("maps chart/tape to watch, ladder to focused, news to interest", () => {
    expect(PANELS.chart.demand).toBe("watch");
    expect(PANELS.tape.demand).toBe("watch");
    expect(PANELS.ladder.demand).toBe("focused");
    expect(PANELS.news.demand).toBe("interest");
  });
  it("leaves non-symbol panels without a demand profile", () => {
    expect(PANELS.scanner?.demand).toBeUndefined();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: FAIL — `demand` is not a property.

- [ ] **Step 3: Add the field and entries**

In `ui/src/chrome/panels/registry.tsx`, import the type and extend `PanelDef`:

```ts
import type { DemandProfile } from "../../wire/DemandRegistry";
```
```ts
export interface PanelDef {
  component: FC<PanelProps>;
  topics: TopicName[];
  title: string;
  glyph: string;
  description: string;
  symbolBearing: boolean;
  demand?: DemandProfile;
}
```

Add `demand` to the four entries:

```ts
  "chart": { component: ChartPanel, topics: ["md.bars", "md.indicator"],
    title: "Chart", glyph: "▁▃▅▇", description: "Candles, volume, indicators",
    symbolBearing: true, demand: "watch" },
  "ladder": { component: LadderPanel, topics: ["md.book", "md.tape", "exec.orders"],
    title: "DOM Ladder", glyph: "≡", description: "10-level depth, working orders",
    symbolBearing: true, demand: "focused" },
  "tape": { component: TapePanel, topics: ["md.tape"],
    title: "Time & Sales", glyph: "⋮⋮", description: "Live prints, buy/sell colored",
    symbolBearing: true, demand: "watch" },
  "news": { component: NewsPanel, topics: ["news.item"],
    title: "News", glyph: "¶", description: "Headlines for focused symbol",
    symbolBearing: true, demand: "interest" },
```

- [ ] **Step 4: Run to verify PASS**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/earl.savadera/Projects/eTape && git add ui/src/chrome/panels/registry.tsx ui/src/chrome/panels/registry.test.ts
git commit -m "feat(ui): panel demand profiles (chart/tape watch, ladder focused, news interest)"
```

---

## Task 9: `DemandRegistry`

**Files:**
- Create: `ui/src/wire/DemandRegistry.ts`
- Test: `ui/src/wire/DemandRegistry.test.ts`

**Interfaces:**
- Produces: `type DemandProfile = "watch" | "focused" | "interest"`; `class DemandRegistry` with `ensure(panelId, symbol, profile): Promise<AckMsg>`, `release(panelId): void`, and reconnect re-announce; constructor takes a `DemandClient` (`sendCommand` + `onState`).
- Consumes: `AckMsg` from `./contract`, `ConnState` from `./WsClient`.

- [ ] **Step 1: Write the failing tests**

Create `ui/src/wire/DemandRegistry.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { DemandRegistry } from "./DemandRegistry";
import type { AckMsg } from "./contract";
import type { ConnState } from "./WsClient";

function fakeClient() {
  const sent: { name: string; args: any }[] = [];
  let stateCb: ((s: ConnState) => void) | null = null;
  let nextAck: AckMsg = { kind: "ack", corrId: "", status: "accepted" };
  return {
    sent,
    setAck: (a: AckMsg) => { nextAck = a; },
    fireState: (s: ConnState) => stateCb?.(s),
    client: {
      sendCommand: (name: string, args: unknown) => { sent.push({ name, args }); return Promise.resolve(nextAck); },
      onState: (cb: (s: ConnState) => void) => { stateCb = cb; },
    },
  };
}

describe("DemandRegistry", () => {
  it("ensure sends EnsureSymbol and records on accept", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    const ack = await reg.ensure("p1", "US.AAPL", "watch");
    expect(ack.status).toBe("accepted");
    expect(f.sent).toEqual([{ name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } }]);
  });

  it("dedupes an unchanged symbol+profile without sending", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    const ack = await reg.ensure("p1", "US.AAPL", "watch");
    expect(ack.status).toBe("accepted");
    expect(f.sent.length).toBe(1); // second call is a no-op
  });

  it("re-sends on a symbol switch", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p1", "US.MSFT", "watch");
    expect(f.sent.length).toBe(2);
  });

  it("does not record on a blocked ack (so a retry re-sends)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    f.setAck({ kind: "ack", corrId: "", status: "blocked", reason: "unknown symbol US.X" });
    const ack = await reg.ensure("p1", "US.X", "watch");
    expect(ack.status).toBe("blocked");
    f.setAck({ kind: "ack", corrId: "", status: "accepted" });
    await reg.ensure("p1", "US.X", "watch");
    expect(f.sent.length).toBe(2); // not deduped — first was never recorded
  });

  it("release sends ReleaseSymbol and forgets", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    reg.release("p1");
    expect(f.sent.at(-1)).toEqual({ name: "ReleaseSymbol", args: { demandId: "p1" } });
    // releasing an unknown panel is a no-op
    reg.release("nope");
    expect(f.sent.length).toBe(2);
  });

  it("re-announces every live demand on reconnect (state=open)", async () => {
    const f = fakeClient();
    const reg = new DemandRegistry(f.client);
    await reg.ensure("p1", "US.AAPL", "watch");
    await reg.ensure("p2", "US.MSFT", "focused");
    f.sent.length = 0;
    f.fireState("open");
    expect(f.sent).toEqual([
      { name: "EnsureSymbol", args: { demandId: "p1", symbol: "US.AAPL", profile: "watch" } },
      { name: "EnsureSymbol", args: { demandId: "p2", symbol: "US.MSFT", profile: "focused" } },
    ]);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/wire/DemandRegistry.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `DemandRegistry`**

Create `ui/src/wire/DemandRegistry.ts`:

```ts
import type { AckMsg } from "./contract";
import type { ConnState } from "./WsClient";

export type DemandProfile = "watch" | "focused" | "interest";

interface DemandClient {
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  onState(cb: (s: ConnState) => void): void;
}

const ACCEPTED: AckMsg = { kind: "ack", corrId: "", status: "accepted" };

// DemandRegistry drives per-panel EnsureSymbol/ReleaseSymbol commands and keeps
// the UI as the source of truth for what's on screen: engine demand state is
// in-memory, so on reconnect (WS drop or engine restart) we re-announce every
// live demand. Owned by App.tsx alongside LinkGroups; one instance per client
// connection (demands are connection-scoped engine-side, so multi-window needs
// no coordination).
export class DemandRegistry {
  private readonly live = new Map<string, { symbol: string; profile: DemandProfile }>();

  constructor(private readonly client: DemandClient) {
    this.client.onState((s) => {
      if (s === "open") this.reannounce();
    });
  }

  // ensure subscribes a panel's symbol. Returns the ack so a gated commit path
  // can revert on rejection. An unchanged symbol+profile is a no-op that
  // resolves accepted (the engine ensure is an idempotent upsert anyway).
  async ensure(panelId: string, symbol: string, profile: DemandProfile): Promise<AckMsg> {
    const cur = this.live.get(panelId);
    if (cur && cur.symbol === symbol && cur.profile === profile) return ACCEPTED;
    const ack = await this.client.sendCommand("EnsureSymbol", { demandId: panelId, symbol, profile });
    if (ack.status === "accepted") this.live.set(panelId, { symbol, profile });
    return ack;
  }

  release(panelId: string): void {
    if (!this.live.has(panelId)) return;
    this.live.delete(panelId);
    void this.client.sendCommand("ReleaseSymbol", { demandId: panelId });
  }

  private reannounce(): void {
    for (const [panelId, { symbol, profile }] of this.live) {
      void this.client.sendCommand("EnsureSymbol", { demandId: panelId, symbol, profile });
    }
  }
}
```

- [ ] **Step 4: Run to verify PASS**

Run: `cd ui && npx vitest run src/wire/DemandRegistry.test.ts`
Expected: PASS (6 cases).

- [ ] **Step 5: Commit**

```bash
cd /Users/earl.savadera/Projects/eTape && git add ui/src/wire/DemandRegistry.ts ui/src/wire/DemandRegistry.test.ts
git commit -m "feat(ui): DemandRegistry — per-panel ensure/release with reconnect re-announce"
```

---

## Task 10: Wire `DemandRegistry` through `PanelFrame` (effects + pinned-commit gate)

**Files:**
- Modify: `ui/src/App.tsx`
- Modify: `ui/src/chrome/AppShell.tsx`
- Modify: `ui/src/chrome/PanelFrame.tsx`
- Test: `ui/src/chrome/PanelFrame.test.tsx` (add cases)

**Interfaces:**
- Consumes: `DemandRegistry` (Task 9), `PanelDef.demand` (Task 8).
- Produces: `PanelFrame` prop `demandRegistry: DemandRegistry`; ensure-on-symbol-change + release-on-unmount effects; pinned type-to-load commit gated on the ensure ack.

- [ ] **Step 1a (REQUIRED harness change — do this first): extend `renderFrame` in `PanelFrame.test.tsx`**

Verified facts about the existing harness (`ui/src/chrome/PanelFrame.test.tsx`, `// @vitest-environment jsdom` at line 1):
- The render helper is **`renderFrame(opts?)`** (NOT `renderPanel`), and it hardcodes `panelId: "news"`, `config.id: "m-news"`, and does **not** pass a `demandRegistry` prop.
- The keydown driver is **`typeKey(key, mods?)`** (`fireEvent.keyDown(document, {...})`), called once per character then `typeKey("Enter")`. `waitFor` is already imported from `@testing-library/react`.
- `news` gets `demand: "interest"` in Task 8, so once `PanelFrame` requires `demandRegistry` and runs the ensure effect, **every existing `renderFrame` test that has a truthy symbol would throw** (`demandRegistry` undefined). This harness change is mandatory, not optional — without it ~20 existing tests break.

Extend `renderFrame`'s options with `panelId?` (default `"news"`) and `demandRegistry?` (default a no-op **accepting** stub), and derive `config.id` from `panelId`:

```ts
// inside renderFrame(opts): default a no-op registry so existing tests keep passing.
const demandRegistry = (opts.demandRegistry ?? {
  ensure: () => Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }),
  release: () => {},
}) as unknown as import("../wire/DemandRegistry").DemandRegistry;
const panelId = opts.panelId ?? "news";
const config: PanelConfig = { id: `m-${panelId}`, panelId, group: opts.group === undefined ? "green" : opts.group, settings: opts.settings ?? {} };
// …pass demandRegistry={demandRegistry} into <PanelFrame …/>.
```

Add `panelId?: string` and `demandRegistry?: import("../wire/DemandRegistry").DemandRegistry` to `renderFrame`'s options type.

- [ ] **Step 1b: Write the failing tests (using the real `renderFrame`/`typeKey`)**

```ts
it("ensures the effective symbol on mount for a demand panel", async () => {
  const calls: { m: string; args: any[] }[] = [];
  const reg = {
    ensure: (...a: any[]) => { calls.push({ m: "ensure", args: a }); return Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }); },
    release: (...a: any[]) => { calls.push({ m: "release", args: a }); },
  } as unknown as import("../wire/DemandRegistry").DemandRegistry;
  renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg });
  await waitFor(() => expect(calls.some((c) => c.m === "ensure")).toBe(true));
  expect(calls.find((c) => c.m === "ensure")!.args).toEqual(["m-chart", "US.AAPL", "watch"]);
});

it("releases on unmount", () => {
  const calls: { m: string; args: any[] }[] = [];
  const reg = {
    ensure: () => Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }),
    release: (...a: any[]) => { calls.push({ m: "release", args: a }); },
  } as unknown as import("../wire/DemandRegistry").DemandRegistry;
  const { unmount } = renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg });
  unmount();
  expect(calls.some((c) => c.m === "release" && c.args[0] === "m-chart")).toBe(true);
});

it("pinned commit reverts on a blocked ensure ack", async () => {
  const ensure = vi.fn()
    .mockResolvedValueOnce({ kind: "ack", corrId: "", status: "accepted" })  // mount ensure (US.AAPL)
    .mockResolvedValueOnce({ kind: "ack", corrId: "", status: "blocked", reason: "unknown symbol US.ZZZZ" });
  const reg = { ensure, release: () => {} } as unknown as import("../wire/DemandRegistry").DemandRegistry;
  const onConfigChange = vi.fn();
  renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg, onConfigChange });
  typeKey("z"); typeKey("z"); typeKey("z"); typeKey("z"); typeKey("Enter");
  await waitFor(() => expect(ensure).toHaveBeenCalledWith("m-chart", "US.ZZZZ", "watch"));
  expect(onConfigChange).not.toHaveBeenCalledWith(expect.objectContaining({ symbol: "US.ZZZZ" }));
});
```

> `normalizeSymbol` uppercases + prefixes bare tickers, so `"zzzz"` → `"US.ZZZZ"`. A pinned `chart` panel uses the demand-gated commit path (grouped panels still use `linkGroups.focusChecked`). Ensure `vi` is imported (the file already imports from `vitest`).

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/PanelFrame.test.tsx`
Expected: FAIL — `demandRegistry` prop unknown / effects absent.

- [ ] **Step 3: Add the prop to `PanelFrame`**

In `ui/src/chrome/PanelFrame.tsx`, import the type and add the prop:

```ts
import type { DemandRegistry } from "../wire/DemandRegistry";
```

Extend the destructured props and the prop type (add `demandRegistry`):

```tsx
export function PanelFrame(
  { config, stores, scheduler, linkGroups, demandRegistry, commands, onConfigChange, onGroupChange, onClose, api }: {
    config: PanelConfig; stores: Stores; scheduler: Scheduler;
    linkGroups: LinkGroups; demandRegistry: DemandRegistry; commands: PanelProps["commands"];
    onConfigChange: (settings: Record<string, unknown>) => void;
    onGroupChange: (group: LinkGroup) => void;
    onClose: () => void;
    api: DockviewPanelApi;
  },
): JSX.Element {
```

- [ ] **Step 4: Add the ensure/release effects**

In `PanelFrame.tsx`, after the `effectiveSymbol` derivation (right after the `const effectiveSymbol = …` line, ~line 128), add:

```tsx
  // On-demand subscription. When this panel declares a demand profile, ask the
  // engine to subscribe the effective (full, prefixed) symbol. ensure is an
  // upsert keyed by this panel's id, so a symbol switch swaps the demand with
  // no explicit release (the old symbol's subs enter the engine's ~5-min
  // hysteresis window). DemandRegistry dedupes an unchanged symbol, so this
  // fires no redundant command when the pinned commit path already ensured.
  useEffect(() => {
    if (!def?.demand || !rawSymbol) return;
    demandRegistry.ensure(config.id, rawSymbol, def.demand).then(
      (ack) => {
        if (ack.status !== "accepted") {
          toast.push({ level: "warn", text: `${bareSymbol(rawSymbol)} — ${ack.reason ?? "unavailable"}` });
        }
      },
      (err) => {
        toast.push({ level: "danger", text: `${bareSymbol(rawSymbol)} failed — ${err instanceof Error ? err.message : "unexpected error"}` });
      },
    );
  }, [def?.demand, rawSymbol, config.id, demandRegistry, toast]);

  // Release exactly once on unmount (not on symbol switch — switches upsert).
  useEffect(() => {
    if (!def?.demand) return;
    return () => demandRegistry.release(config.id);
  }, [def?.demand, config.id, demandRegistry]);
```

- [ ] **Step 5: Gate the pinned commit path**

In the `commit` function (inside the keydown effect), replace the pinned branch so a demand panel validates before applying:

```tsx
        if (group !== null) {
          const r = await linkGroups.focusChecked(group, sym);
          if (!r.ok) toast.push({ level: "danger", text: `${sym} rejected — ${r.reason}` });
          return;
        }
        if (def?.demand) {
          const ack = await demandRegistry.ensure(config.id, sym, def.demand);
          if (ack.status !== "accepted") {
            toast.push({ level: "danger", text: `${sym} rejected — ${ack.reason ?? "unknown symbol"}` });
            return; // leave the prior symbol untouched — no half-loaded pinned panel
          }
        }
        onConfigChange({ ...config.settings, symbol: sym });
```

Add `demandRegistry`, `config.id`, and `def?.demand` to the keydown effect's dependency array (currently ends `… toast, config.settings]`):

```tsx
  }, [active, modalOpen, group, def?.symbolBearing, def?.demand, linkGroups, demandRegistry, onConfigChange, toast, config.id, config.settings]);
```

- [ ] **Step 6: Thread the prop through `AppShell` and `App`**

In `ui/src/chrome/AppShell.tsx`:

```ts
import type { DemandRegistry } from "../wire/DemandRegistry";
```
```ts
interface Props {
  workspaceName: string;
  stores: Stores;
  scheduler: Scheduler;
  workspaceStore: WorkspaceStore;
  linkGroups: LinkGroups;
  demandRegistry: DemandRegistry;
  commands: PanelProps["commands"];
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore, linkGroups, demandRegistry, commands }: Props): JSX.Element {
```

And in the `components` factory (the `<PanelFrame … />` instantiation):

```tsx
      (panelProps: IDockviewPanelProps) => <PanelFrame config={p} stores={stores} scheduler={scheduler}
        linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands}
        onConfigChange={(settings) => onConfigChange(p.id, settings)}
        onGroupChange={(group) => onGroupChange(p.id, group)}
        onClose={() => removePanel(p.id)}
        api={panelProps.api} />,
```

In `ui/src/App.tsx`:

```ts
import { DemandRegistry } from "./wire/DemandRegistry";
```

Inside the `useMemo`, construct it and return it:

```ts
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), (group, symbol) =>
      client.sendCommand("FocusGroup", { group, symbol }),
    );
    const demandRegistry = new DemandRegistry(client);
    return { client, stores, scheduler, workspaceStore, linkGroups, demandRegistry };
  }, []);
```

Update the destructure and the `<AppShell … />` props:

```ts
  const { client, stores, scheduler, workspaceStore, linkGroups, demandRegistry } = useMemo(() => {
```
```tsx
              <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
                workspaceStore={workspaceStore} linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} />
```

- [ ] **Step 7: Run PanelFrame tests + typecheck + build**

Run: `cd ui && npx vitest run src/chrome/PanelFrame.test.tsx`
Expected: PASS (existing type-to-load tests still green + new demand cases).

Run: `cd ui && npm run typecheck`
Expected: no type errors (all three files thread the prop; `typecheck` = `tsc -p tsconfig.json --noEmit && tsc -p tsconfig.node.json --noEmit`).

Run: `cd ui && npm run build`
Expected: build succeeds (`build` runs `typecheck` then `vite build`).

- [ ] **Step 8: Commit**

```bash
cd /Users/earl.savadera/Projects/eTape && git add ui/src/App.tsx ui/src/chrome/AppShell.tsx ui/src/chrome/PanelFrame.tsx ui/src/chrome/PanelFrame.test.tsx
git commit -m "feat(ui): panels drive on-demand subscription via DemandRegistry"
```

---

## Final verification

- [ ] **Engine full suite:** `cd engine && go test -race ./... && go vet ./...` → all pass.
- [ ] **tygo drift:** `cd engine && make gen-ts-check` → exit 0.
- [ ] **UI full suite:** `cd ui && npm test` → all pass. (If the canvas-touching files flake when batched, run them individually — known vitest forks-pool quirk.)
- [ ] **UI typecheck + build:** `cd ui && npm run build` → clean (runs `typecheck` — both tsconfig projects — then `vite build`).
- [ ] **E2E regression (replay):** the existing Playwright type-to-load flows are the regression net; replay's nil-feed accept-everything behavior means EnsureSymbol/ReleaseSymbol/FocusGroup all ack accepted and no new scenarios are required. Run the existing E2E suite per the repo's E2E command and confirm no regressions.
- [ ] **Manual live smoke (optional, live OpenD):** boot the engine live, open a chart panel, type a valid symbol → panel loads (subscribed on demand); type a garbage symbol → toast "unknown symbol …", panel keeps the prior symbol. Confirm the news poller picks up a symbol only present via an on-demand panel (log/observe `ActiveDemandSymbols`).

---

## Out of scope (from the spec — do not implement)

- Symbols with open positions are **not** auto-demanded (pre-existing gap; closing every panel on a position freezes its P&L marks until re-subscribed).
- Starvation surfacing in the health panel (`subman.Slots()`/`Starved()` stay unconsumed).
- Broker-selection / longer-grace UX beyond the existing `unsub_hysteresis_secs` config knob.
- Declarative per-connection set-sync, workspace-JSON-inferred demands, always-Focused, and sticky-for-session demands (all rejected alternatives).
