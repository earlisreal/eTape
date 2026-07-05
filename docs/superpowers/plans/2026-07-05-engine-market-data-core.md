# eTape Engine — Plan 2 of 6: Market-Data Core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the engine's entire market-data plane on top of Plan 1's OpenD client: the broker-agnostic `feed` interface + event union, the ET `session` calendar with session-anchored bar bucketing, the subscription/quota manager (the single owner of `Qot_Sub`), the cache/history backfill layer, and the single-writer `md` core — books, tape ring, quotes, bars (10s-from-ticks, authoritative 1m from `K_1M`, 5m–60m aggregated, daily fetched, W/M derived) and the six v1 indicators — race-free, deterministic (`replay(events) == state`), and verified live against OpenD.

**Architecture:** Strict dependency direction continues: new domain packages `feed`, `session`, `md` never import the adapter (`feed/opend`). The adapter translates OpenD pushes and cache reads into `feed.Event` values on one channel; the `md` core is a single goroutine that consumes that channel plus control messages, owns all market-data state, does no I/O and never reads the wall clock (event timestamps only), and emits typed updates + last-trade marks on outbound channels. Everything I/O-shaped (client pump, subscription worker, seed fetches) is its own goroutine that only passes messages. `go test -race` enforces the discipline.

**Tech Stack:** Go 1.25 (module from Plan 1), `google.golang.org/protobuf` (generated `pb/` from Plan 1 — no regeneration needed), `time/tzdata` (embedded IANA zones), existing mock-OpenD test harness. Python 3 + moomoo SDK only for golden-frame capture (Task 15).

## Global Constraints

Copied from the approved specs and Plan 1. Every task's requirements implicitly include this section.

- **Module path:** `github.com/earlisreal/eTape/engine`; this plan builds on the Plan 1 code on branch `worktree-engine-foundation-opend` (worktree `.claude/worktrees/engine-foundation-opend`) — Plan 1 is implemented there and **not yet merged to main**. Execute Plan 2 on that branch (or a branch cut from it). Never touch the main checkout's `ui/` directory (owned by the concurrent UI session).
- **Dependency rule:** domain packages (`feed`, `md`, `session`, later `exec`) never import adapters (`feed/opend`, `broker/*`), `uihub`, or `store`. `feed/opend` may import `feed`, `session`, `clock`. (go-engine-design §Dependency rule)
- **Single-writer core:** exactly one goroutine (md core `Run` loop) touches market-data state. No mutexes in the domain; concurrency only at I/O edges. The apply path does no I/O and never calls `time.Now` — all decisions use event timestamps, so replaying a journal reproduces state exactly. (go-engine-design §Single-writer core, §md Invariant)
- **Trade-incapability rule:** the feed connection implements **no `Trd_*` protocols**, ever. This plan touches only `Qot_*` and system protoIDs. (CLAUDE.md)
- **US-first scope:** session times are ET (`America/New_York`): pre 04:00–09:30, RTH 09:30–16:00, post 16:00–20:00. Subscriptions set `ExtendedTime=true`. Symbols use the moomoo-prefixed form **`US.AAPL`** as the canonical domain string (the UI already renders these; `HK.`/`CC.` prefixes work for dev smoke tests only). (CLAUDE.md scope decision; ui plans)
- **Exchange timestamps are authoritative** for all bucketing; receive time is only for latency metrics. Prefer the protobuf `Timestamp` (epoch seconds, float64) fields; never parse `Time` strings for bucketing. (go-engine-design §Error handling)
- **K-line time labeling (verified live 2026-07-05):** moomoo intraday K-lines label the bucket **END** (`US.AAPL` RTH day = 390 bars, `09:31:00`…`16:00:00`, closing auction volume on the `16:00:00` bar). eTape's canonical bar key is the bucket **START**, so the adapter decoder subtracts one span (60 s for 1m). Daily bars are labeled with their own date — no shift. This normalization lives in exactly one function (`decodeKLine`).
- **Bar bucketing must match the UI test-mirror byte-for-byte** (`ui/src/render/chart/barBucket.ts`): 10s/1m floor on the minute grid from ET wall-midnight; 5m/15m/30m/60m floored against the 09:30 ET anchor (floored division — pre-market buckets have negative offsets); D = ET wall-midnight; W = Monday 00:00 ET; M = 1st of month 00:00 ET. Timeframe strings: `"10s" "1m" "5m" "15m" "30m" "60m" "D" "W" "M"`. Anchor configurable (default 09:30).
- **Quota rules (API_LIMITS.md, verified numbers):** subscriptions cost 1 slot per (symbol, subtype), base tier 100 slots; ≥60 s before unsubscribe; batch subscribes are ~50 ms **per call**, not per symbol. Historical K-line: 1 slot per unique **symbol** in 30 days (all periods of one symbol = 1 slot; re-requests within 30 days are free), base tier 100; `request_history_kline` pages at ≤1,000 bars via `NextReqKey`. Cache reads are quota-free: `get_cur_kline` ≤1,000 bars (~9 ms), `get_rt_ticker` ≤1,000 ticks (~30 ms), `get_order_book` ≤10 levels (~2.5 ms). With `ExtendedTime=true` a full US day is 960 1m bars — the 1,000-bar cache covers ~1 day, so deeper intraday history needs the history API.
- **Demand profiles:** focused symbol = QUOTE+ORDER_BOOK+TICKER+K_1M (4 slots); watchlist symbol = TICKER+K_1M (2 slots). Released symbols get delayed unsubscribe: min-hold 60 s **and** hysteresis (default 5 min). Under budget pressure evict least-recently-demanded non-focused symbols and warn. (go-engine-design §Subscription manager)
- **Honesty policy:** never render stale as live — gaps are flagged, never interpolated; dropped updates are counted and logged; decode failures are counted, never silently ignored. (go-engine-design §Error handling)
- **Repo is PUBLIC; sensitive-sweep every commit.** Market-data frames carry no account data and are safe to commit as golden fixtures; keep excluding `InitConnect` (1001) and `GetGlobalState` (1002) S2C frames (PII / server IPs). (Plan 1 constraint)
- **Determinism:** anything time-dependent takes `clock.Clock`. The md core takes **no clock at all**. (go-engine-design §Clock)
- **CI gates:** `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run` (v2 config from Plan 1) all pass at every task boundary.

---

## Plan sequence context

This is **Plan 2 of 6** (roadmap in Plan 1's header: 1 Foundation/OpenD client → **2 Market-data core** → 3 Store/journal/replay → 4 Execution core → 5 Broker adapters → 6 uihub/pollers/main). Plan 2's deliverable: *the engine ingests live OpenD and maintains books/tape/bars/indicators, race-free and deterministic, verified by unit + property tests* — plus a `cmd/etape` harness that proves it against live OpenD.

**Consumed from Plan 1:** `opend.Client` (`Request`, `Pushes() <-chan Frame`, `State() <-chan ConnState`, `Run`), `opend.Frame`/`Encode`, protoID constants, generated `pb/` for all protos, `clock.Clock`, `config.Load`, mock-OpenD test harness, golden-frame capture script.

**Produced for later plans:** `feed.Feed` + `feed.Event` (Plan 3 tees this exact stream into the journal; `replay` reimplements `feed.Feed`), `md.Core` updates/marks channels (Plan 6 uihub consumes updates; Plan 4 exec consumes marks), `session` calendar (Plan 6 scanner/pollers), `clock.Fake` (pulled forward from the Plan 3 roadmap note — Plan 2's hysteresis tests need it; Plan 3 just uses it).

**Deviations from the roadmap flagged:** `clock.Fake` lands here, not Plan 3. `Qot_GetSecuritySnapshot` (3203) is deferred to Plan 6 (scanner float data — nothing in Plan 2 needs it). `Qot_GetRT`/`Qot_UpdateRT` (3008/3009) are the *time-share curve*, not ticks — eTape never subscribes RT; recent ticks come from `Qot_GetTicker` (3010).

---

## File Structure (Plan 2)

```
engine/
  internal/
    clock/
      fake.go                     clock   — deterministic Fake clock (Advance-driven)
      fake_test.go
    session/
      session.go                  session — ET calendar: Loc, Phase, Timeframe, bucketing
      session_test.go
    feed/
      feed.go                     feed    — domain types, SubType, Demand, Feed interface
      events.go                   feed    — Event union (Ticks/Quote/Book/Bars1m/Conn/Resynced)
      feed_test.go
    feed/opend/                           (existing package — extended)
      pending.go                  MODIFY  — protoID-aware correlation
      client.go                   MODIFY  — push-protoID routing in reader
      protoid.go                  MODIFY  — IsPushProtoID + push set
      decode.go                   NEW     — pb → feed domain decoders, symbol mapping
      decode_test.go              NEW
      subman.go                   NEW     — subscription/quota manager (the Qot_Sub owner)
      subman_test.go              NEW
      backfill.go                 NEW     — cache reads + history K-line + quota guard
      backfill_test.go            NEW
      opendfeed.go                NEW     — OpenDFeed: implements feed.Feed
      opendfeed_test.go           NEW
      mock_opend_test.go          MODIFY  — qot handlers, nonzero push serials
    md/
      core.go                     md      — single-writer Core: inbox, Run, outputs
      update.go                   md      — Update union, Mark, md.Bar
      book.go  quote.go  tape.go  md      — latest-book / latest-quote / tick ring
      tickagg.go                  md      — tick→bar aggregator (10s + shadow 1m)
      bars.go                     md      — barEngine: 1m auth, 5–60m, daily, W/M, gaps
      indicator.go                md      — instance registry + calc contract
      ind_calcs.go                md      — VWAP, EMA, SMA, MACD, VOLUME, DELTA
      core_test.go  tape_test.go  tickagg_test.go  bars_test.go
      indicator_test.go  determinism_test.go
    config/
      config.go                   MODIFY  — [feed] and [md] sections
      config_test.go              MODIFY
  cmd/etape/
    main.go                       MODIFY  — wire client → OpenDFeed → md.Core, log updates
  scripts/
    capture_golden_frames.py      MODIFY  — qot capture mode (Task 15)
```

---

## Task 1: Fix push-vs-request serial collision in the `opend` client

**Required before any subscription flows** (Plan 1 post-implementation addendum): responses are currently correlated by serialNo alone, but real OpenD pushes carry an independent nonzero server-side serial (observed pushes ~1517–1519 vs requests ~2752–2758). Once Plan 2 subscribes, a push serial can collide with an in-flight request serial and be mis-delivered to that request's waiter.

**Files:**
- Modify: `engine/internal/feed/opend/protoid.go`
- Modify: `engine/internal/feed/opend/pending.go`
- Modify: `engine/internal/feed/opend/client.go` (reader loop + `Request`)
- Modify: `engine/internal/feed/opend/mock_opend_test.go` (nonzero push serials)
- Modify: `engine/internal/feed/opend/pending_test.go`, `engine/internal/feed/opend/client_test.go`

**Interfaces:**
- Consumes: Plan 1's `pending`, `Client.Request`, reader goroutine in `serveConn`.
- Produces: `IsPushProtoID(id uint32) bool`; `pending.register(serial, protoID uint32) chan Frame`; `pending.resolve(f Frame) bool` (matches on serial **and** protoID). `cancel`/`failAll` unchanged. Mock helper becomes `push(conn net.Conn, protoID, serialNo uint32, msg proto.Message)`.

- [ ] **Step 1: Write the failing regression test**

In `engine/internal/feed/opend/client_test.go` add:

```go
// TestPushSerialCollisionDoesNotHijackRequest reproduces the live observation
// that OpenD pushes carry their own serial numbers: a push whose serial equals
// an in-flight request's serial must NOT resolve that request.
func TestPushSerialCollisionDoesNotHijackRequest(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(m *mockOpenD, conn net.Conn, f Frame) {
		switch f.ProtoID {
		case ProtoInitConnect, ProtoKeepAlive:
			m.defaultHandler(m, conn, f)
		case ProtoQotGetBasicQot:
			// Adversarial ordering: first a ticker push reusing the request's
			// serial, then the real response.
			m.push(conn, ProtoQotUpdateTicker, f.SerialNo, &qotupdateticker.Response{
				RetType: proto.Int32(0),
				S2C:     &qotupdateticker.S2C{Security: &qotcommon.Security{Market: proto.Int32(11), Code: proto.String("AAPL")}},
			})
			m.reply(conn, f, &qotgetbasicqot.Response{RetType: proto.Int32(0), S2C: &qotgetbasicqot.S2C{}})
		}
	}

	c := New(Options{Addr: m.addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	waitForState(t, c, ConnUp)

	f, err := c.Request(ctx, ProtoQotGetBasicQot, &qotgetbasicqot.Request{C2S: &qotgetbasicqot.C2S{}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if f.ProtoID != ProtoQotGetBasicQot {
		t.Fatalf("request resolved with protoID %d (hijacked by push), want %d", f.ProtoID, ProtoQotGetBasicQot)
	}
	select {
	case p := <-c.Pushes():
		if p.ProtoID != ProtoQotUpdateTicker {
			t.Fatalf("push protoID = %d, want %d", p.ProtoID, ProtoQotUpdateTicker)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push frame never delivered to Pushes()")
	}
}
```

Add the imports (`qotcommon`, `qotgetbasicqot`, `qotupdateticker` from `.../pb/`). Plan 1's tests await `ConnUp` inline; add this shared helper to `client_test.go` (Tasks 7–8 reuse it):

```go
// waitForState blocks until the client reports the wanted state (draining
// intermediate transitions) or fails the test after 3s.
func waitForState(t *testing.T, c *Client, want ConnState) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case st := <-c.State():
			if st == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for connection state %v", want)
		}
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `cd engine && go test -race ./internal/feed/opend/ -run TestPushSerialCollision -v`
Expected: FAIL — mock `push` has no serial parameter yet (compile error), and after the signature fix the request resolves with the push frame (`protoID 3011, want 3004`).

First update the mock so the test compiles: change `push` to take a serial and update existing call sites (pass any nonzero value, e.g. `1500`):

```go
// push sends an unsolicited frame. Real OpenD pushes carry an independent
// nonzero server-side serial — the mock must too, or tests can't catch
// serial-collision bugs.
func (m *mockOpenD) push(conn net.Conn, protoID, serialNo uint32, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(protoID, serialNo, body))
}
```

- [ ] **Step 3: Make correlation protoID-aware**

`engine/internal/feed/opend/protoid.go` — append:

```go
// pushProtoIDs are server-initiated update protocols. A frame with one of
// these IDs is never a response, no matter what its serialNo says — real
// OpenD pushes carry an independent server-side serial that can collide with
// an in-flight request serial (observed live 2026-07-05).
var pushProtoIDs = map[uint32]struct{}{
	ProtoQotUpdateBasicQot:  {},
	ProtoQotUpdateKL:        {},
	ProtoQotUpdateRT:        {},
	ProtoQotUpdateTicker:    {},
	ProtoQotUpdateOrderBook: {},
}

// IsPushProtoID reports whether protoID is a known push protocol.
func IsPushProtoID(id uint32) bool {
	_, ok := pushProtoIDs[id]
	return ok
}
```

`engine/internal/feed/opend/pending.go` — store the expected protoID with each waiter:

```go
package opend

import "sync"

// pending correlates responses to in-flight requests by serial number AND
// protoID. Every method removes its entry from the map under the lock before
// touching the channel, so failAll can never close a channel that
// resolve/cancel is about to use.
type pending struct {
	mu sync.Mutex
	m  map[uint32]waiter
}

type waiter struct {
	protoID uint32
	ch      chan Frame
}

func newPending() *pending { return &pending{m: make(map[uint32]waiter)} }

// register reserves a slot for serial and returns the (buffered) delivery channel.
func (p *pending) register(serial, protoID uint32) chan Frame {
	ch := make(chan Frame, 1)
	p.mu.Lock()
	p.m[serial] = waiter{protoID: protoID, ch: ch}
	p.mu.Unlock()
	return ch
}

// resolve delivers f to the waiter for f.SerialNo, but only when the waiter's
// protoID also matches. It returns false if no matching waiter is registered —
// the caller then treats f as a push.
func (p *pending) resolve(f Frame) bool {
	p.mu.Lock()
	w, ok := p.m[f.SerialNo]
	if ok && w.protoID == f.ProtoID {
		delete(p.m, f.SerialNo)
		p.mu.Unlock()
		w.ch <- f // cap-1 buffer: never blocks
		return true
	}
	p.mu.Unlock()
	return false
}

// cancel drops a waiter (e.g. after a timeout). Idempotent.
func (p *pending) cancel(serial uint32) {
	p.mu.Lock()
	delete(p.m, serial)
	p.mu.Unlock()
}

// failAll closes every outstanding waiter — used on disconnect so blocked
// Request calls unblock and return ErrNotConnected.
func (p *pending) failAll() {
	p.mu.Lock()
	for s, w := range p.m {
		close(w.ch)
		delete(p.m, s)
	}
	p.mu.Unlock()
}
```

`engine/internal/feed/opend/client.go` — two edits. In `Request`, pass the protoID:

```go
	serial := c.serial.next()
	ch := c.pending.register(serial, protoID)
```

In `serveConn`'s reader goroutine, route known push protoIDs straight to `Pushes()` and let `resolve` check both keys:

```go
			if IsPushProtoID(f.ProtoID) || !c.pending.resolve(f) {
				select {
				case c.pushes <- f:
				case <-sctx.Done():
					return
				default:
					// push buffer full: drop. The feed wrapper owns
					// coalescing/backpressure and forces a re-snapshot instead.
				}
			}
```

Update `pending_test.go` for the new signatures (`register(serial, protoID)`; `resolve(Frame{SerialNo: s, ProtoID: p})`) and add one case: registered waiter for (serial=7, protoID=1004) does **not** resolve a frame (serial=7, protoID=3011) and `resolve` returns false.

- [ ] **Step 4: Run the full package to verify it passes**

Run: `cd engine && go test -race ./internal/feed/opend/ -v`
Expected: PASS, including the new regression test and all Plan 1 tests.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/opend/protoid.go internal/feed/opend/pending.go internal/feed/opend/client.go internal/feed/opend/pending_test.go internal/feed/opend/client_test.go internal/feed/opend/mock_opend_test.go
git commit -m "fix(engine/opend): protoID-aware response correlation; pushes never hijack requests"
```

---

## Task 2: `clock.Fake` — deterministic time for timer-driven tests

Pulled forward from the Plan 3 roadmap note: Plan 2's subscription-manager tests (60 s min-hold, 5 min hysteresis) are impossible against the real clock.

**Files:**
- Create: `engine/internal/clock/fake.go`
- Test: `engine/internal/clock/fake_test.go`

**Interfaces:**
- Consumes: `clock.Clock`, `clock.Ticker` (Plan 1).
- Produces: `clock.NewFake(start time.Time) *Fake` implementing `clock.Clock`, plus `(*Fake).Advance(d time.Duration)` which moves time forward and fires every due `After`/`Ticker` deterministically, in chronological order.

- [ ] **Step 1: Write the failing test**

`engine/internal/clock/fake_test.go`:

```go
package clock

import (
	"testing"
	"time"
)

func TestFakeAfterFiresOnAdvance(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	ch := f.After(5 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired before Advance")
	default:
	}
	f.Advance(4 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(time.Second)
	select {
	case ts := <-ch:
		if !ts.Equal(time.Unix(1005, 0)) {
			t.Fatalf("fired at %v, want %v", ts, time.Unix(1005, 0))
		}
	default:
		t.Fatal("did not fire at deadline")
	}
	if !f.Now().Equal(time.Unix(1005, 0)) {
		t.Fatalf("Now = %v, want 1005", f.Now())
	}
}

func TestFakeTickerRepeatsAndStops(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	tk := f.NewTicker(10 * time.Second)
	f.Advance(35 * time.Second)
	// Ticker channels have capacity 1 (matching time.Ticker): 3 ticks were due
	// but only the earliest undelivered one is buffered.
	select {
	case <-tk.C():
	default:
		t.Fatal("no tick buffered after 35s")
	}
	tk.Stop()
	f.Advance(time.Minute)
	select {
	case <-tk.C():
		t.Fatal("tick after Stop")
	default:
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/clock/ -run TestFake -v`
Expected: FAIL — `undefined: NewFake`.

- [ ] **Step 3: Implement `Fake`**

`engine/internal/clock/fake.go`:

```go
package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake is a deterministic Clock for tests: time moves only when Advance is
// called, and every due timer/ticker fires in chronological order during the
// Advance call. Channels have capacity 1, matching time.Ticker semantics —
// an unconsumed tick is dropped, never queued.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	wakers []*fakeWaker
}

type fakeWaker struct {
	at       time.Time
	interval time.Duration // 0 = one-shot After
	ch       chan time.Time
	stopped  bool
}

// NewFake returns a Fake clock frozen at start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &fakeWaker{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.wakers = append(f.wakers, w)
	return w.ch
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &fakeWaker{at: f.now.Add(d), interval: d, ch: make(chan time.Time, 1)}
	f.wakers = append(f.wakers, w)
	return fakeTicker{f: f, w: w}
}

// Advance moves the clock forward by d, firing due wakers in time order.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	for {
		var next *fakeWaker
		for _, w := range f.wakers {
			if w.stopped || w.at.After(target) {
				continue
			}
			if next == nil || w.at.Before(next.at) {
				next = w
			}
		}
		if next == nil {
			break
		}
		f.now = next.at
		select {
		case next.ch <- next.at:
		default: // undelivered tick: drop, like time.Ticker
		}
		if next.interval > 0 {
			next.at = next.at.Add(next.interval)
		} else {
			next.stopped = true
		}
	}
	f.now = target
	// Compact stopped one-shots so long tests don't accumulate garbage.
	live := f.wakers[:0]
	for _, w := range f.wakers {
		if !w.stopped {
			live = append(live, w)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].at.Before(live[j].at) })
	f.wakers = live
}

type fakeTicker struct {
	f *Fake
	w *fakeWaker
}

func (t fakeTicker) C() <-chan time.Time { return t.w.ch }
func (t fakeTicker) Stop() {
	t.f.mu.Lock()
	t.w.stopped = true
	t.f.mu.Unlock()
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/clock/ -v`
Expected: PASS (both new tests + Plan 1's).

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/clock/fake.go internal/clock/fake_test.go
git commit -m "feat(engine/clock): deterministic Fake clock (pulled forward from Plan 3)"
```

## Task 3: `session` — ET calendar + session-anchored bucketing

Pure functions, no I/O, no clock. Must agree byte-for-byte with the UI's test-mirror (`ui/src/render/chart/barBucket.ts` — reuse its test vectors).

**Files:**
- Create: `engine/internal/session/session.go`
- Test: `engine/internal/session/session_test.go`

**Interfaces:**
- Consumes: nothing (stdlib + embedded tzdata only).
- Produces:
  - `type Timeframe string` with constants `TF10s "10s"`, `TF1m "1m"`, `TF5m "5m"`, `TF15m "15m"`, `TF30m "30m"`, `TF60m "60m"`, `TFDay "D"`, `TFWeek "W"`, `TFMonth "M"`.
  - `func IntradaySpanSecs(tf Timeframe) (int64, bool)` — 10/60/300/900/1800/3600; false for D/W/M.
  - `func Loc() *time.Location` — `America/New_York`, embedded tzdata.
  - `type Phase int` (`Closed`, `PreMarket`, `RTH`, `PostMarket`) + `func PhaseAt(t time.Time) Phase`.
  - `const AnchorSecsDefault int64 = 34200` (09:30 ET).
  - `func BucketStartMs(tsMs int64, tf Timeframe) int64` and `func BucketStartMsAnchored(tsMs int64, tf Timeframe, anchorSecs int64) int64`.
  - `func DayMs(tsMs int64) int64` — ET wall-midnight of the calendar day containing tsMs (the D bucket; used by VWAP's day-reset and tick-sequence day-reset).

- [ ] **Step 1: Write the failing test**

`engine/internal/session/session_test.go` — the UTC instants are the UI mirror's vectors plus EST/pre-market/W/M cases:

```go
package session

import (
	"testing"
	"time"
)

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestBucketStartMs(t *testing.T) {
	cases := []struct {
		name string
		ts   string
		tf   Timeframe
		want string
	}{
		// EDT (UTC-4): 2026-07-06 13:30 UTC = 09:30 ET (same vectors as the UI mirror).
		{"10s floor", "2026-07-06T13:30:07Z", TF10s, "2026-07-06T13:30:00Z"},
		{"10s second bucket", "2026-07-06T13:30:12Z", TF10s, "2026-07-06T13:30:10Z"},
		{"1m floor", "2026-07-06T13:30:45Z", TF1m, "2026-07-06T13:30:00Z"},
		// 5m anchored at 09:30 ET.
		{"5m at open", "2026-07-06T13:32:00Z", TF5m, "2026-07-06T13:30:00Z"},
		{"5m second bucket", "2026-07-06T13:36:00Z", TF5m, "2026-07-06T13:35:00Z"},
		// Pre-market: negative offsets from the anchor must floor toward -inf.
		// 08:12 ET → 5m bucket 08:10 ET (12:10 UTC), 60m bucket 08:30 ET? No:
		// 08:12 is in [07:30,08:30) relative to the 09:30 anchor → 07:30 ET.
		{"5m pre-market", "2026-07-06T12:12:00Z", TF5m, "2026-07-06T12:10:00Z"},
		{"60m pre-market", "2026-07-06T12:12:00Z", TF60m, "2026-07-06T11:30:00Z"},
		{"60m RTH", "2026-07-06T14:45:00Z", TF60m, "2026-07-06T14:30:00Z"},
		// EST (UTC-5): 2026-01-06 14:30 UTC = 09:30 ET.
		{"1m in EST", "2026-01-06T14:31:30Z", TF1m, "2026-01-06T14:31:00Z"},
		{"30m in EST", "2026-01-06T15:10:00Z", TF30m, "2026-01-06T15:00:00Z"},
		// D/W/M: wall-midnight ET.
		{"D", "2026-07-06T18:00:00Z", TFDay, "2026-07-06T04:00:00Z"},
		{"W from Thursday", "2026-07-09T18:00:00Z", TFWeek, "2026-07-06T04:00:00Z"},
		{"W on Monday", "2026-07-06T18:00:00Z", TFWeek, "2026-07-06T04:00:00Z"},
		{"M mid-month", "2026-07-17T18:00:00Z", TFMonth, "2026-07-01T04:00:00Z"},
	}
	for _, c := range cases {
		if got := BucketStartMs(ms(c.ts), c.tf); got != ms(c.want) {
			t.Errorf("%s: BucketStartMs(%s, %s) = %d (%s), want %s",
				c.name, c.ts, c.tf, got, time.UnixMilli(got).UTC().Format(time.RFC3339), c.want)
		}
	}
}

func TestBucketStartMsCustomAnchor(t *testing.T) {
	// Anchor 09:00 ET: 09:20 ET falls in [09:00, 10:00).
	got := BucketStartMsAnchored(ms("2026-07-06T13:20:00Z"), TF60m, 9*3600)
	if want := ms("2026-07-06T13:00:00Z"); got != want {
		t.Fatalf("anchored bucket = %d, want %d", got, want)
	}
}

func TestPhaseAt(t *testing.T) {
	cases := []struct {
		ts   string
		want Phase
	}{
		{"2026-07-06T08:00:00Z", PreMarket},  // 04:00 ET Monday
		{"2026-07-06T13:29:59Z", PreMarket},  // 09:29:59 ET
		{"2026-07-06T13:30:00Z", RTH},        // 09:30 ET
		{"2026-07-06T19:59:59Z", RTH},        // 15:59:59 ET
		{"2026-07-06T20:00:00Z", PostMarket}, // 16:00 ET
		{"2026-07-07T00:00:00Z", Closed},     // 20:00 ET
		{"2026-07-06T07:59:59Z", Closed},     // 03:59:59 ET
		{"2026-07-04T15:00:00Z", Closed},     // Saturday
		{"2026-07-05T15:00:00Z", Closed},     // Sunday
		{"2026-01-06T14:30:00Z", RTH},        // EST regime
	}
	for _, c := range cases {
		if got := PhaseAt(time.UnixMilli(ms(c.ts))); got != c.want {
			t.Errorf("PhaseAt(%s) = %v, want %v", c.ts, got, c.want)
		}
	}
}

func TestDayMs(t *testing.T) {
	if got, want := DayMs(ms("2026-07-06T18:00:00Z")), ms("2026-07-06T04:00:00Z"); got != want {
		t.Fatalf("DayMs = %d, want %d", got, want)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/session/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`engine/internal/session/session.go`:

```go
// Package session is the pure ET (America/New_York) trading calendar: session
// phases (pre 04:00–09:30, RTH 09:30–16:00, post 16:00–20:00) and
// session-anchored bar bucketing. It is DST-correct by construction and must
// stay in exact agreement with the UI's test-mirror
// (ui/src/render/chart/barBucket.ts) — both use wall-clock-seconds arithmetic
// from ET midnight, which is self-consistent for market hours (no US session
// straddles the 02:00 DST transition).
package session

import (
	"time"
	_ "time/tzdata" // embed IANA zones: correctness must not depend on host zoneinfo
)

// Timeframe is a chart timeframe key. Values match the UI contract exactly.
type Timeframe string

const (
	TF10s   Timeframe = "10s"
	TF1m    Timeframe = "1m"
	TF5m    Timeframe = "5m"
	TF15m   Timeframe = "15m"
	TF30m   Timeframe = "30m"
	TF60m   Timeframe = "60m"
	TFDay   Timeframe = "D"
	TFWeek  Timeframe = "W"
	TFMonth Timeframe = "M"
)

// Intraday lists the intraday timeframes in ascending span order.
var Intraday = []Timeframe{TF10s, TF1m, TF5m, TF15m, TF30m, TF60m}

// IntradaySpanSecs returns the bucket span for intraday timeframes and
// ok=false for calendar timeframes (D/W/M).
func IntradaySpanSecs(tf Timeframe) (int64, bool) {
	switch tf {
	case TF10s:
		return 10, true
	case TF1m:
		return 60, true
	case TF5m:
		return 5 * 60, true
	case TF15m:
		return 15 * 60, true
	case TF30m:
		return 30 * 60, true
	case TF60m:
		return 60 * 60, true
	}
	return 0, false
}

// AnchorSecsDefault is the default intraday bucket anchor: 09:30 ET.
const AnchorSecsDefault int64 = 9*3600 + 30*60

var loc = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic("session: America/New_York missing from embedded tzdata: " + err.Error())
	}
	return l
}()

// Loc returns the America/New_York location.
func Loc() *time.Location { return loc }

// Phase is a point-in-time session classification.
type Phase int

const (
	Closed Phase = iota
	PreMarket
	RTH
	PostMarket
)

func (p Phase) String() string {
	switch p {
	case PreMarket:
		return "pre"
	case RTH:
		return "rth"
	case PostMarket:
		return "post"
	}
	return "closed"
}

// PhaseAt classifies t. Weekends are Closed; US market holidays are NOT
// modeled in v1 (a holiday reads as a normal weekday with no data).
func PhaseAt(t time.Time) Phase {
	et := t.In(loc)
	if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return Closed
	}
	s := wallSecs(et)
	switch {
	case s >= 4*3600 && s < AnchorSecsDefault:
		return PreMarket
	case s >= AnchorSecsDefault && s < 16*3600:
		return RTH
	case s >= 16*3600 && s < 20*3600:
		return PostMarket
	}
	return Closed
}

func wallSecs(et time.Time) int64 {
	return int64(et.Hour())*3600 + int64(et.Minute())*60 + int64(et.Second())
}

// dayMidnightMs mirrors the UI's etMidnightMs: subtract the ET wall-clock
// seconds from ts. Self-consistent within a trading day even across DST
// regimes (documented caveat shared with the UI mirror).
func dayMidnightMs(tsMs int64) int64 {
	et := time.UnixMilli(tsMs).In(loc)
	return tsMs - wallSecs(et)*1000 - int64(et.Nanosecond()/1e6)
}

// DayMs returns the D bucket (ET wall-midnight) containing tsMs.
func DayMs(tsMs int64) int64 { return dayMidnightMs(tsMs) }

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// BucketStartMs buckets tsMs into tf with the default 09:30 ET anchor.
func BucketStartMs(tsMs int64, tf Timeframe) int64 {
	return BucketStartMsAnchored(tsMs, tf, AnchorSecsDefault)
}

// BucketStartMsAnchored buckets tsMs into tf. 10s/1m align to the minute grid;
// 5m–60m floor against anchorSecs (pre-market offsets are negative — floored
// division, matching the UI mirror); D/W/M are calendar buckets.
func BucketStartMsAnchored(tsMs int64, tf Timeframe, anchorSecs int64) int64 {
	midnight := dayMidnightMs(tsMs)
	secsIntoDay := (tsMs - midnight) / 1000
	floorTo := func(span, anchor int64) int64 {
		rel := secsIntoDay - anchor
		return midnight + (anchor+floorDiv(rel, span)*span)*1000
	}
	switch tf {
	case TF10s:
		return floorTo(10, 0)
	case TF1m:
		return floorTo(60, 0)
	case TF5m, TF15m, TF30m, TF60m:
		span, _ := IntradaySpanSecs(tf)
		return floorTo(span, anchorSecs)
	case TFDay:
		return midnight
	case TFWeek:
		et := time.UnixMilli(tsMs).In(loc)
		daysFromMonday := (int64(et.Weekday()) + 6) % 7
		return midnight - daysFromMonday*86_400_000
	case TFMonth:
		et := time.UnixMilli(tsMs).In(loc)
		return midnight - int64(et.Day()-1)*86_400_000
	}
	return midnight
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/session/ -v`
Expected: PASS — all bucket vectors (both DST regimes), phases, DayMs.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/session/
git commit -m "feat(engine/session): ET calendar + session-anchored bucketing (UI-mirror-exact)"
```

---

## Task 4: `feed` — broker-agnostic domain types, event union, `Feed` interface

The seam every later plan builds on: Plan 3's journal tees `feed.Event`; `replay` reimplements `feed.Feed`; the md core consumes only these types.

**Files:**
- Create: `engine/internal/feed/feed.go`
- Create: `engine/internal/feed/events.go`
- Test: `engine/internal/feed/feed_test.go`

**Interfaces:**
- Consumes: stdlib only (`context`, `time`). **No imports of `session` or adapters** — `feed` stays at the bottom of the domain graph.
- Produces (consumed verbatim by Tasks 5–14):

```go
type Direction uint8            // Neutral, Buy, Sell
type Tick struct {              // one trade print
	Symbol   string             // canonical prefixed form, e.g. "US.AAPL"
	Seq      int64              // exchange sequence (unique per symbol per day)
	TsMs     int64              // exchange timestamp, epoch ms — authoritative
	Price    float64
	Volume   int64
	Turnover float64
	Dir      Direction
	RecvTsMs int64              // OpenD receive time — latency metrics only
}
type Quote struct { Symbol string; TsMs int64; Last, Open, High, Low, PrevClose float64; Volume int64; Turnover float64 }
type BookLevel struct { Price float64; Volume int64; Orders int32 }
type Book struct { Symbol string; TsMs int64; Bids, Asks []BookLevel }
type Bar struct { Symbol string; BucketMs int64; O, H, L, C float64; Volume int64; Turnover float64 }

type Event interface{ isEvent() }
type TicksEvent struct { Ticks []Tick; Seed bool }
type QuoteEvent struct { Quote Quote; Seed bool }
type BookEvent struct { Book Book; Seed bool }
type Bars1mEvent struct { Bars []Bar; Seed bool }
type ConnUpEvent struct{}
type ConnDownEvent struct{}
type ResyncedEvent struct{}

type SubType uint8              // SubQuote, SubBook, SubTicker, SubKL1m
type Demand struct { ID, Symbol string; Subs []SubType; Focused bool }
func FocusedDemand(id, symbol string) Demand
func WatchDemand(id, symbol string) Demand

type Resolution uint8           // Res1m, ResDay
type Feed interface {
	Events() <-chan Event
	Ensure(d Demand)
	Release(id string)
	HistoryBars(ctx context.Context, symbol string, res Resolution, from, to time.Time) ([]Bar, error)
	RecentTicks(ctx context.Context, symbol string, n int) ([]Tick, error)
	CachedBars1m(ctx context.Context, symbol string, n int) ([]Bar, error)
	BookSnapshot(ctx context.Context, symbol string) (Book, error)
	QuoteSnapshot(ctx context.Context, symbol string) (Quote, error)
}
```

- [ ] **Step 1: Write the failing test**

`engine/internal/feed/feed_test.go`:

```go
package feed

import "testing"

func TestDirectionString(t *testing.T) {
	for d, want := range map[Direction]string{Buy: "BUY", Sell: "SELL", Neutral: "NEUTRAL"} {
		if got := d.String(); got != want {
			t.Errorf("Direction(%d).String() = %q, want %q", d, got, want)
		}
	}
}

func TestDemandProfiles(t *testing.T) {
	f := FocusedDemand("chart-1", "US.AAPL")
	if !f.Focused || len(f.Subs) != 4 {
		t.Fatalf("focused profile = %+v, want 4 subs, Focused", f)
	}
	w := WatchDemand("watch-AAPL", "US.AAPL")
	if w.Focused || len(w.Subs) != 2 {
		t.Fatalf("watch profile = %+v, want 2 subs, not Focused", w)
	}
	// Watch profile is exactly TICKER + K_1M (tape/10s/1m recording, no depth).
	if w.Subs[0] != SubTicker || w.Subs[1] != SubKL1m {
		t.Fatalf("watch subs = %v, want [SubTicker SubKL1m]", w.Subs)
	}
}

// Compile-time exhaustiveness: every event type is part of the union.
var _ = []Event{
	TicksEvent{}, QuoteEvent{}, BookEvent{}, Bars1mEvent{},
	ConnUpEvent{}, ConnDownEvent{}, ResyncedEvent{},
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/feed/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`engine/internal/feed/feed.go`:

```go
// Package feed defines the broker-agnostic market-data domain: tick, quote,
// book and bar types, the FeedEvent union, subscription demands, and the Feed
// interface implemented by feed/opend (live) and replay (Plan 3, journal).
// It sits at the bottom of the domain graph and imports nothing but stdlib.
package feed

import (
	"context"
	"time"
)

// Direction is the aggressor side of a trade print.
type Direction uint8

const (
	Neutral Direction = iota
	Buy
	Sell
)

func (d Direction) String() string {
	switch d {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	}
	return "NEUTRAL"
}

// Tick is one trade print. TsMs is the exchange timestamp (authoritative for
// bucketing); RecvTsMs is OpenD receive time, used only for latency metrics.
type Tick struct {
	Symbol   string
	Seq      int64
	TsMs     int64
	Price    float64
	Volume   int64
	Turnover float64
	Dir      Direction
	RecvTsMs int64
}

// Quote is the latest basic quote. moomoo's BasicQot carries no bid/ask —
// top-of-book comes from Book; the md core composes the two.
type Quote struct {
	Symbol    string
	TsMs      int64
	Last      float64
	Open      float64
	High      float64
	Low       float64
	PrevClose float64
	Volume    int64
	Turnover  float64
}

// BookLevel is one price level of one side.
type BookLevel struct {
	Price  float64
	Volume int64
	Orders int32
}

// Book is a full replacement snapshot of the visible depth (10 levels on US
// LV3). TsMs is OpenD's server receive time — display only, never bucketing.
type Book struct {
	Symbol string
	TsMs   int64
	Bids   []BookLevel
	Asks   []BookLevel
}

// Bar is a raw OHLCV bar keyed by its bucket START (epoch ms). The adapter
// normalizes moomoo's end-labeled intraday K-lines before they reach here.
type Bar struct {
	Symbol   string
	BucketMs int64
	O, H, L, C float64
	Volume   int64
	Turnover float64
}

// SubType is a broker-agnostic subscription kind.
type SubType uint8

const (
	SubQuote SubType = iota
	SubBook
	SubTicker
	SubKL1m
)

// Demand is a consumer's declaration of interest. The subscription manager
// refcounts demands; a symbol's live subscriptions are the union of demands.
type Demand struct {
	ID      string
	Symbol  string
	Subs    []SubType
	Focused bool // focused symbols survive LRU eviction under quota pressure
}

// FocusedDemand is the focused-symbol profile: full depth + tape + bars
// (4 quota slots).
func FocusedDemand(id, symbol string) Demand {
	return Demand{ID: id, Symbol: symbol, Focused: true,
		Subs: []SubType{SubQuote, SubBook, SubTicker, SubKL1m}}
}

// WatchDemand is the watchlist profile: tape/10s/1m recording, no depth
// (2 quota slots).
func WatchDemand(id, symbol string) Demand {
	return Demand{ID: id, Symbol: symbol,
		Subs: []SubType{SubTicker, SubKL1m}}
}

// Resolution selects a history series.
type Resolution uint8

const (
	Res1m Resolution = iota
	ResDay
)

// Feed is the adapter-agnostic market-data source. Events() is the single
// stream the md core consumes and the journal (Plan 3) tees; queries are
// blocking request/response.
type Feed interface {
	Events() <-chan Event
	Ensure(d Demand)
	Release(id string)
	HistoryBars(ctx context.Context, symbol string, res Resolution, from, to time.Time) ([]Bar, error)
	RecentTicks(ctx context.Context, symbol string, n int) ([]Tick, error)
	CachedBars1m(ctx context.Context, symbol string, n int) ([]Bar, error)
	BookSnapshot(ctx context.Context, symbol string) (Book, error)
	QuoteSnapshot(ctx context.Context, symbol string) (Quote, error)
}
```

`engine/internal/feed/events.go`:

```go
package feed

// Event is the sealed union of everything a Feed emits. Seed=true marks
// backfill-derived events (cache reads on subscribe/reconnect) — the md core
// applies them through the identical path as live events (dedup handles
// overlap), and the journal (Plan 3) records the flag.
type Event interface{ isEvent() }

// TicksEvent carries one push (or seed batch) of trade prints, oldest first.
type TicksEvent struct {
	Ticks []Tick
	Seed  bool
}

// QuoteEvent replaces the symbol's latest quote.
type QuoteEvent struct {
	Quote Quote
	Seed  bool
}

// BookEvent replaces the symbol's book.
type BookEvent struct {
	Book Book
	Seed bool
}

// Bars1mEvent carries authoritative 1m bars (K_1M push or cache seed),
// oldest first.
type Bars1mEvent struct {
	Bars []Bar
	Seed bool
}

// ConnUpEvent / ConnDownEvent are feed-connection transitions.
// ResyncedEvent fires after a reconnect once re-subscription and cache
// re-seeding are complete — consumers may re-snapshot.
type ConnUpEvent struct{}
type ConnDownEvent struct{}
type ResyncedEvent struct{}

func (TicksEvent) isEvent()    {}
func (QuoteEvent) isEvent()    {}
func (BookEvent) isEvent()     {}
func (Bars1mEvent) isEvent()   {}
func (ConnUpEvent) isEvent()   {}
func (ConnDownEvent) isEvent() {}
func (ResyncedEvent) isEvent() {}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/feed/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/feed.go internal/feed/events.go internal/feed/feed_test.go
git commit -m "feat(engine/feed): broker-agnostic MD types, event union, Feed interface"
```

## Task 5: `feed/opend` decoders — pb → domain, symbol mapping, K-line normalization

The only place moomoo wire shapes become domain values. Includes the verified end-labeled-K-line fix and the `OrederCount` generated-typo workaround.

**Files:**
- Create: `engine/internal/feed/opend/decode.go`
- Test: `engine/internal/feed/opend/decode_test.go`

**Interfaces:**
- Consumes: `feed` types (Task 4), generated `pb/` packages (`qotcommon`, `qotupdateticker`, `qotupdatebasicqot`, `qotupdateorderbook`, `qotupdatekl`), `Frame`, protoID constants.
- Produces (used by Tasks 6–8):
  - `func parseSymbol(sym string) (*qotcommon.Security, error)` — `"US.AAPL"` → `{Market:11, Code:"AAPL"}`; bare `"AAPL"` defaults to US; `HK.`→1, `CC.`→91, `SH.`→21, `SZ.`→22 supported for dev smoke.
  - `func formatSymbol(sec *qotcommon.Security) string` — inverse; canonical prefixed form.
  - `func tsMs(sec float64) int64` — epoch-seconds float → epoch ms (rounded).
  - `func decodeTicker(symbol string, t *qotcommon.Ticker) feed.Tick`
  - `func decodeBasicQot(b *qotcommon.BasicQot) (feed.Quote, error)`
  - `func decodeBookLevels(list []*qotcommon.OrderBook) []feed.BookLevel`
  - `func decodeKLine(symbol string, k *qotcommon.KLine, res feed.Resolution) (feed.Bar, error)` — **the single normalization point**: `Res1m` subtracts 60 000 ms (end-labeled, verified 2026-07-05); `ResDay` uses the timestamp as-is; `Timestamp == 0` is an error (honesty over guessing).
  - `func DecodePush(f Frame) ([]feed.Event, error)` — routes 3005/3007/3011/3013 to events; 3009 (RT) and unknown IDs return `(nil, nil)`; a non-zero `RetType` or unmarshal failure returns an error.

- [ ] **Step 1: Write the failing test**

`engine/internal/feed/opend/decode_test.go`:

```go
package opend

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatekl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

func sec(market int32, code string) *qotcommon.Security {
	return &qotcommon.Security{Market: proto.Int32(market), Code: proto.String(code)}
}

func TestSymbolRoundTrip(t *testing.T) {
	cases := []struct {
		in     string
		market int32
		code   string
		out    string
	}{
		{"US.AAPL", 11, "AAPL", "US.AAPL"},
		{"AAPL", 11, "AAPL", "US.AAPL"}, // bare defaults to US, canonicalizes
		{"HK.00700", 1, "00700", "HK.00700"},
		{"CC.BTC", 91, "BTC", "CC.BTC"},
	}
	for _, c := range cases {
		s, err := parseSymbol(c.in)
		if err != nil {
			t.Fatalf("parseSymbol(%q): %v", c.in, err)
		}
		if s.GetMarket() != c.market || s.GetCode() != c.code {
			t.Errorf("parseSymbol(%q) = (%d,%q), want (%d,%q)", c.in, s.GetMarket(), s.GetCode(), c.market, c.code)
		}
		if got := formatSymbol(s); got != c.out {
			t.Errorf("formatSymbol(parseSymbol(%q)) = %q, want %q", c.in, got, c.out)
		}
	}
	if _, err := parseSymbol("XX.FOO"); err == nil {
		t.Error("unknown market prefix must error")
	}
}

func TestDecodeKLineNormalizesEndLabel(t *testing.T) {
	// Verified live 2026-07-05: intraday K-lines label the bucket END
	// (US.AAPL RTH = 390 bars 09:31:00..16:00:00). 2026-07-02 16:00:00 ET
	// = 2026-07-02T20:00:00Z = epoch 1783022400 → the bar COVERS
	// 15:59–16:00, bucket start 15:59.
	k := &qotcommon.KLine{
		Timestamp: proto.Float64(1783022400), Time: proto.String("2026-07-02 16:00:00"),
		OpenPrice: proto.Float64(308.75), HighPrice: proto.Float64(309.2),
		LowPrice: proto.Float64(308.5), ClosePrice: proto.Float64(308.63),
		Volume: proto.Int64(12551689), Turnover: proto.Float64(3.87e9),
	}
	b, err := decodeKLine("US.AAPL", k, feed.Res1m)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(1783022400_000 - 60_000); b.BucketMs != want {
		t.Fatalf("1m BucketMs = %d, want %d (end label minus one span)", b.BucketMs, want)
	}
	if b.O != 308.75 || b.C != 308.63 || b.Volume != 12551689 {
		t.Fatalf("bar values wrong: %+v", b)
	}

	day, err := decodeKLine("US.AAPL", &qotcommon.KLine{
		Timestamp: proto.Float64(1782964800), // 2026-07-02 00:00:00 ET (= 04:00Z)
		OpenPrice: proto.Float64(1), HighPrice: proto.Float64(1), LowPrice: proto.Float64(1), ClosePrice: proto.Float64(1),
	}, feed.ResDay)
	if err != nil {
		t.Fatal(err)
	}
	if day.BucketMs != 1782964800_000 {
		t.Fatalf("daily BucketMs shifted: %d", day.BucketMs) // day-labeled: no shift
	}

	if _, err := decodeKLine("US.AAPL", &qotcommon.KLine{}, feed.Res1m); err == nil {
		t.Fatal("zero Timestamp must be a decode error, not a guess")
	}
}

func TestDecodePushTicker(t *testing.T) {
	resp := &qotupdateticker.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateticker.S2C{
			Security: sec(11, "AAPL"),
			TickerList: []*qotcommon.Ticker{{
				Sequence: proto.Int64(7001), Timestamp: proto.Float64(1782146000.123),
				Price: proto.Float64(309.10), Volume: proto.Int64(200), Turnover: proto.Float64(61820),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid)),
			}, {
				Sequence: proto.Int64(7002), Timestamp: proto.Float64(1782146000.500),
				Price: proto.Float64(309.05), Volume: proto.Int64(100), Turnover: proto.Float64(30905),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Ask)),
			}},
		},
	}
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateTicker, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	te, ok := evs[0].(feed.TicksEvent)
	if !ok || len(te.Ticks) != 2 {
		t.Fatalf("got %#v, want TicksEvent with 2 ticks", evs)
	}
	t0 := te.Ticks[0]
	if t0.Symbol != "US.AAPL" || t0.Seq != 7001 || t0.TsMs != 1782146000123 || t0.Dir != feed.Buy {
		t.Fatalf("tick[0] = %+v", t0)
	}
	if te.Ticks[1].Dir != feed.Sell {
		t.Fatalf("tick[1].Dir = %v, want Sell", te.Ticks[1].Dir)
	}
}

func TestDecodePushBookUsesTypoField(t *testing.T) {
	resp := &qotupdateorderbook.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateorderbook.S2C{
			Security: sec(11, "AAPL"),
			OrderBookBidList: []*qotcommon.OrderBook{
				{Price: proto.Float64(309.00), Volume: proto.Int64(500), OrederCount: proto.Int32(4)},
			},
			OrderBookAskList: []*qotcommon.OrderBook{
				{Price: proto.Float64(309.02), Volume: proto.Int64(300), OrederCount: proto.Int32(2)},
			},
			SvrRecvTimeBidTimestamp: proto.Float64(1782146001.0),
			SvrRecvTimeAskTimestamp: proto.Float64(1782146001.5),
		},
	}
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateOrderBook, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	be := evs[0].(feed.BookEvent)
	if be.Book.Bids[0].Orders != 4 || be.Book.Asks[0].Volume != 300 {
		t.Fatalf("book = %+v", be.Book)
	}
	if be.Book.TsMs != 1782146001500 { // max(bid, ask) server recv time
		t.Fatalf("book TsMs = %d", be.Book.TsMs)
	}
}

func TestDecodePushKLFiltersNon1m(t *testing.T) {
	mk := func(klType qotcommon.KLType) []byte {
		resp := &qotupdatekl.Response{
			RetType: proto.Int32(0),
			S2C: &qotupdatekl.S2C{
				KlType: proto.Int32(int32(klType)), Security: sec(11, "AAPL"),
				KlList: []*qotcommon.KLine{{
					Timestamp: proto.Float64(1782146460), OpenPrice: proto.Float64(1),
					HighPrice: proto.Float64(1), LowPrice: proto.Float64(1), ClosePrice: proto.Float64(1),
				}},
			},
		}
		b, _ := proto.Marshal(resp)
		return b
	}
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateKL, Body: mk(qotcommon.KLType_KLType_1Min)})
	if err != nil || len(evs) != 1 {
		t.Fatalf("1m push: evs=%v err=%v", evs, err)
	}
	evs, err = DecodePush(Frame{ProtoID: ProtoQotUpdateKL, Body: mk(qotcommon.KLType_KLType_Day)})
	if err != nil || len(evs) != 0 {
		t.Fatalf("non-1m KL push must be ignored: evs=%v err=%v", evs, err)
	}
	// Unknown push IDs and RT pushes are ignored, not errors.
	if evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateRT, Body: nil}); err != nil || evs != nil {
		t.Fatalf("RT push: evs=%v err=%v", evs, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/feed/opend/ -run TestSymbol -run TestDecode -v` (or just the package)
Expected: FAIL — `parseSymbol`, `decodeKLine`, `DecodePush` undefined.

- [ ] **Step 3: Implement**

`engine/internal/feed/opend/decode.go`:

```go
package opend

import (
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatebasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatekl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

// marketByPrefix maps eTape's canonical symbol prefixes to QotMarket values.
// US is the product scope; HK/CC/SH/SZ exist for dev smoke tests only
// (weekends: CC trades 24/7, HK LV1 covers quotes+ticker+1-level book).
var marketByPrefix = map[string]int32{
	"US": int32(qotcommon.QotMarket_QotMarket_US_Security),   // 11
	"HK": int32(qotcommon.QotMarket_QotMarket_HK_Security),   // 1
	"SH": int32(qotcommon.QotMarket_QotMarket_CNSH_Security), // 21
	"SZ": int32(qotcommon.QotMarket_QotMarket_CNSZ_Security), // 22
	"CC": int32(qotcommon.QotMarket_QotMarket_CC_Security),   // 91
}

var prefixByMarket = func() map[int32]string {
	m := make(map[int32]string, len(marketByPrefix))
	for p, id := range marketByPrefix {
		m[id] = p
	}
	return m
}()

// parseSymbol converts a domain symbol ("US.AAPL"; bare defaults to US) to a
// pb Security.
func parseSymbol(sym string) (*qotcommon.Security, error) {
	prefix, code, found := strings.Cut(sym, ".")
	if !found {
		prefix, code = "US", sym
	}
	market, ok := marketByPrefix[prefix]
	if !ok {
		return nil, fmt.Errorf("opend: unknown market prefix %q in symbol %q", prefix, sym)
	}
	if code == "" {
		return nil, fmt.Errorf("opend: empty code in symbol %q", sym)
	}
	return &qotcommon.Security{Market: proto.Int32(market), Code: proto.String(code)}, nil
}

// formatSymbol converts a pb Security back to the canonical prefixed form.
func formatSymbol(s *qotcommon.Security) string {
	if p, ok := prefixByMarket[s.GetMarket()]; ok {
		return p + "." + s.GetCode()
	}
	return fmt.Sprintf("M%d.%s", s.GetMarket(), s.GetCode())
}

// tsMs converts moomoo's epoch-seconds float64 timestamps to epoch ms.
func tsMs(sec float64) int64 { return int64(math.Round(sec * 1000)) }

func decodeDirection(d int32) feed.Direction {
	switch qotcommon.TickerDirection(d) {
	case qotcommon.TickerDirection_TickerDirection_Bid:
		return feed.Buy
	case qotcommon.TickerDirection_TickerDirection_Ask:
		return feed.Sell
	}
	return feed.Neutral
}

func decodeTicker(symbol string, t *qotcommon.Ticker) feed.Tick {
	return feed.Tick{
		Symbol:   symbol,
		Seq:      t.GetSequence(),
		TsMs:     tsMs(t.GetTimestamp()),
		Price:    t.GetPrice(),
		Volume:   t.GetVolume(),
		Turnover: t.GetTurnover(),
		Dir:      decodeDirection(t.GetDir()),
		RecvTsMs: tsMs(t.GetRecvTime()),
	}
}

func decodeBasicQot(b *qotcommon.BasicQot) (feed.Quote, error) {
	if b.GetSecurity() == nil {
		return feed.Quote{}, fmt.Errorf("opend: BasicQot without security")
	}
	return feed.Quote{
		Symbol:    formatSymbol(b.GetSecurity()),
		TsMs:      tsMs(b.GetUpdateTimestamp()),
		Last:      b.GetCurPrice(),
		Open:      b.GetOpenPrice(),
		High:      b.GetHighPrice(),
		Low:       b.GetLowPrice(),
		PrevClose: b.GetLastClosePrice(),
		Volume:    b.GetVolume(),
		Turnover:  b.GetTurnover(),
	}, nil
}

func decodeBookLevels(list []*qotcommon.OrderBook) []feed.BookLevel {
	out := make([]feed.BookLevel, 0, len(list))
	for _, l := range list {
		out = append(out, feed.BookLevel{
			Price:  l.GetPrice(),
			Volume: l.GetVolume(),
			// Generated-code typo in the SDK proto: "OrederCount". Kept as-is —
			// the pb is committed, regeneration would reproduce it anyway.
			Orders: l.GetOrederCount(),
		})
	}
	return out
}

// decodeKLine is the ONLY place moomoo K-line time labeling is normalized.
// Verified live 2026-07-05: intraday K-lines label the bucket END (US.AAPL
// RTH day = 390 bars stamped 09:31:00..16:00:00, closing auction volume on
// the 16:00:00 bar), so Res1m subtracts one 60 s span to get eTape's
// bucket-START key. Daily bars are labeled with their own date — no shift.
// A zero Timestamp is an error: guessing from the Time string would silently
// mis-bucket HK symbols (their Time strings are HK-local).
func decodeKLine(symbol string, k *qotcommon.KLine, res feed.Resolution) (feed.Bar, error) {
	if k.GetTimestamp() == 0 {
		return feed.Bar{}, fmt.Errorf("opend: K-line for %s has no Timestamp (time=%q)", symbol, k.GetTime())
	}
	bucket := tsMs(k.GetTimestamp())
	if res == feed.Res1m {
		bucket -= 60_000
	}
	return feed.Bar{
		Symbol:   symbol,
		BucketMs: bucket,
		O:        k.GetOpenPrice(),
		H:        k.GetHighPrice(),
		L:        k.GetLowPrice(),
		C:        k.GetClosePrice(),
		Volume:   k.GetVolume(),
		Turnover: k.GetTurnover(),
	}, nil
}

// DecodePush converts a push frame into domain events. Unknown protoIDs and
// deliberately-unused pushes (RT time-share) return (nil, nil); malformed
// bodies or non-zero RetType return an error so the caller can count them.
func DecodePush(f Frame) ([]feed.Event, error) {
	switch f.ProtoID {
	case ProtoQotUpdateTicker:
		var resp qotupdateticker.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: ticker push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: ticker push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		symbol := formatSymbol(s2c.GetSecurity())
		ticks := make([]feed.Tick, 0, len(s2c.GetTickerList()))
		for _, t := range s2c.GetTickerList() {
			ticks = append(ticks, decodeTicker(symbol, t))
		}
		if len(ticks) == 0 {
			return nil, nil
		}
		return []feed.Event{feed.TicksEvent{Ticks: ticks}}, nil

	case ProtoQotUpdateBasicQot:
		var resp qotupdatebasicqot.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: basicqot push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: basicqot push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		var evs []feed.Event
		for _, b := range resp.GetS2C().GetBasicQotList() {
			q, err := decodeBasicQot(b)
			if err != nil {
				return nil, err
			}
			evs = append(evs, feed.QuoteEvent{Quote: q})
		}
		return evs, nil

	case ProtoQotUpdateOrderBook:
		var resp qotupdateorderbook.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: book push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: book push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		book := feed.Book{
			Symbol: formatSymbol(s2c.GetSecurity()),
			TsMs:   tsMs(math.Max(s2c.GetSvrRecvTimeBidTimestamp(), s2c.GetSvrRecvTimeAskTimestamp())),
			Bids:   decodeBookLevels(s2c.GetOrderBookBidList()),
			Asks:   decodeBookLevels(s2c.GetOrderBookAskList()),
		}
		return []feed.Event{feed.BookEvent{Book: book}}, nil

	case ProtoQotUpdateKL:
		var resp qotupdatekl.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: kl push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: kl push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		// eTape only subscribes K_1M; ignore anything else defensively.
		if s2c.GetKlType() != int32(qotcommon.KLType_KLType_1Min) {
			return nil, nil
		}
		symbol := formatSymbol(s2c.GetSecurity())
		bars := make([]feed.Bar, 0, len(s2c.GetKlList()))
		for _, k := range s2c.GetKlList() {
			b, err := decodeKLine(symbol, k, feed.Res1m)
			if err != nil {
				return nil, err
			}
			bars = append(bars, b)
		}
		if len(bars) == 0 {
			return nil, nil
		}
		return []feed.Event{feed.Bars1mEvent{Bars: bars}}, nil
	}
	return nil, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -v`
Expected: PASS (all decode tests + Plan 1 + Task 1 tests).

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/opend/decode.go internal/feed/opend/decode_test.go
git commit -m "feat(engine/opend): pb-to-domain decoders, symbol mapping, end-labeled K-line normalization"
```

---

## Task 6: Subscription manager — the quota owner

The single component that issues `Qot_Sub`. Encodes every quota rule: 1 slot per (symbol, subtype) against the budget; ≥60 s min-hold; delayed unsubscribe with hysteresis; batched calls grouped by subtype set; focused symbols survive pressure; least-recently-demanded non-focused symbols are starved first.

**Files:**
- Create: `engine/internal/feed/opend/subman.go`
- Test: `engine/internal/feed/opend/subman_test.go`

**Interfaces:**
- Consumes: `clock.Clock`/`clock.Fake`, `feed.Demand`/`feed.SubType`, `parseSymbol`, `qotsub`/`qotcommon` pb, and an `rpc` seam:

```go
// rpc is the request seam (satisfied by *Client) so the manager and backfill
// are testable without a socket.
type rpc interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error)
}
```

- Produces (used by Task 8):

```go
func newSubManager(r rpc, clk clock.Clock, o subOptions) *subManager
type subOptions struct {
	Budget       int           // quota slots (default 100)
	MinHold      time.Duration // default 60s  (moomoo rule)
	Hysteresis   time.Duration // default 5m   (release delay)
	ExtendedTime bool          // default true (US pre/post)
}
func (m *subManager) Run(ctx context.Context)          // worker loop (own goroutine)
func (m *subManager) Ensure(d feed.Demand)             // upsert demand, kick worker
func (m *subManager) Release(id string)                // drop demand, kick worker
func (m *subManager) ResubscribeAll(ctx context.Context) error // one batch per subtype-group; reconnect path
func (m *subManager) ActiveSymbols() map[string][]feed.SubType // current live subs (reseed set)
func (m *subManager) Slots() int                       // active (symbol,subtype) count
func (m *subManager) Starved() []string                // symbols denied by budget, sorted
```

**Semantics (encode exactly):**
1. *Desired set* = union of subtypes over all demands per symbol, capped by budget: order symbols focused-first, then by most-recent `Ensure` time; accumulate slots until the budget is hit; the rest are **starved** (logged once per change via `slog.Warn`, queryable).
2. Worker pass (triggered by a kick channel and a 1 s ticker): subscribe `desired − active` grouped by identical subtype set (one `Qot_Sub` per group, `IsSubOrUnSub=true, IsRegOrUnRegPush=true, IsFirstPush=true, ExtendedTime=o.ExtendedTime`); unsubscribe active entries that left the desired set only when `now − subscribedAt ≥ MinHold` **and** `now − droppedAt ≥ Hysteresis` (`IsSubOrUnSub=false, IsRegOrUnRegPush=false`). `droppedAt` is stamped by the first pass that observes the entry outside the desired set; re-entering the desired set clears it.
3. **Pressure eviction (the spec's LRU rule):** hysteresis holds slots only while they're free. If `len(active) − removes + adds` would exceed the budget, promote additional lingering entries (dropped, past MinHold) into the unsubscribe batch — oldest `droppedAt` first — until the projection fits. MinHold is never waived (moomoo rejects early unsubscribes); if the projection still exceeds budget, the excess `adds` wait (their symbols stay starved) and the pass retries as holds expire.
4. A `Qot_Sub` failure (error or non-zero `RetType`) leaves state unchanged for that group and logs; the next pass retries. Never panic.
5. `ResubscribeAll` marks all active entries as freshly subscribed (`subscribedAt = now`).

- [ ] **Step 1: Write the failing tests**

`engine/internal/feed/opend/subman_test.go`:

```go
package opend

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

// fakeRPC records Qot_Sub calls and answers success.
type fakeRPC struct {
	mu    sync.Mutex
	calls []*qotsub.C2S
}

func (f *fakeRPC) Request(_ context.Context, protoID uint32, req proto.Message) (Frame, error) {
	if protoID != ProtoQotSub {
		panic("subManager must only send Qot_Sub")
	}
	f.mu.Lock()
	f.calls = append(f.calls, proto.Clone(req.(*qotsub.Request)).(*qotsub.Request).GetC2S())
	f.mu.Unlock()
	body, _ := proto.Marshal(&qotsub.Response{RetType: proto.Int32(0), S2C: &qotsub.S2C{}})
	return Frame{ProtoID: ProtoQotSub, Body: body}, nil
}

func (f *fakeRPC) snapshot() []*qotsub.C2S {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*qotsub.C2S(nil), f.calls...)
}

// pump runs one synchronous worker pass (test seam — see step 3).
func newTestManager(t *testing.T, budget int) (*subManager, *fakeRPC, *clock.Fake) {
	t.Helper()
	rpc := &fakeRPC{}
	clk := clock.NewFake(time.Unix(1_782_000_000, 0))
	m := newSubManager(rpc, clk, subOptions{Budget: budget, MinHold: time.Minute, Hysteresis: 5 * time.Minute, ExtendedTime: true})
	return m, rpc, clk
}

func TestEnsureBatchesAndRefcounts(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w1", "US.AAPL"))
	m.Ensure(feed.WatchDemand("w2", "US.TSLA"))
	m.Ensure(feed.WatchDemand("w1b", "US.AAPL")) // second demand, same symbol
	m.pass(context.Background())

	calls := rpc.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 batched Qot_Sub for one subtype-group, got %d", len(calls))
	}
	c := calls[0]
	if !c.GetIsSubOrUnSub() || !c.GetIsRegOrUnRegPush() || !c.GetIsFirstPush() || !c.GetExtendedTime() {
		t.Fatalf("subscribe flags wrong: %+v", c)
	}
	if len(c.GetSecurityList()) != 2 || len(c.GetSubTypeList()) != 2 {
		t.Fatalf("want 2 symbols x [Ticker,KL1m], got %d symbols x %d subtypes",
			len(c.GetSecurityList()), len(c.GetSubTypeList()))
	}
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots = %d, want 4 (2 symbols x 2 subtypes)", got)
	}
	// Releasing one of AAPL's two demands must not unsubscribe.
	m.Release("w1")
	m.pass(context.Background())
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots after partial release = %d, want 4", got)
	}
}

func TestUnsubscribeWaitsForMinHoldAndHysteresis(t *testing.T) {
	m, rpc, clk := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.pass(context.Background())
	m.Release("w")
	m.pass(context.Background()) // stamps droppedAt (worker's first observation)

	clk.Advance(2 * time.Minute) // past MinHold, inside Hysteresis
	m.pass(context.Background())
	if n := len(rpc.snapshot()); n != 1 {
		t.Fatalf("unsubscribed inside hysteresis window (calls=%d)", n)
	}
	clk.Advance(4 * time.Minute) // 6m since droppedAt: past Hysteresis
	m.pass(context.Background())
	calls := rpc.snapshot()
	if last := calls[len(calls)-1]; last.GetIsSubOrUnSub() {
		t.Fatal("expected an unsubscribe call")
	}
	if got := m.Slots(); got != 0 {
		t.Fatalf("Slots = %d, want 0", got)
	}

	// Re-Ensure inside the window cancels a pending unsubscribe.
	m.Ensure(feed.WatchDemand("w2", "US.MSFT"))
	m.pass(context.Background())
	m.Release("w2")
	m.pass(context.Background()) // droppedAt stamped
	clk.Advance(2 * time.Minute)
	m.Ensure(feed.WatchDemand("w3", "US.MSFT")) // re-desired: cancels the drop
	base := len(rpc.snapshot())
	clk.Advance(10 * time.Minute)
	m.pass(context.Background())
	for _, c := range rpc.snapshot()[base:] {
		if !c.GetIsSubOrUnSub() {
			t.Fatal("MSFT was unsubscribed despite re-Ensure")
		}
	}
	if got := m.Slots(); got != 2 {
		t.Fatalf("Slots = %d, want 2 (MSFT still live)", got)
	}
}

func TestPressureWaivesHysteresisButNeverMinHold(t *testing.T) {
	m, rpc, clk := newTestManager(t, 2) // room for exactly one watch symbol
	m.Ensure(feed.WatchDemand("a", "US.AAA"))
	m.pass(context.Background())
	m.Release("a")
	m.pass(context.Background()) // droppedAt stamped; slots still held

	// New demand needs the held slots. Inside MinHold nothing can move:
	// the add must wait (starved), the lingering subs must survive.
	m.Ensure(feed.WatchDemand("b", "US.BBB"))
	clk.Advance(30 * time.Second)
	m.pass(context.Background())
	if s := m.Starved(); len(s) != 1 || s[0] != "US.BBB" {
		t.Fatalf("Starved = %v, want [US.BBB] while MinHold pins the old slots", s)
	}
	// Past MinHold (but far inside the 5m hysteresis) pressure evicts AAA.
	clk.Advance(31 * time.Second)
	m.pass(context.Background())
	act := m.ActiveSymbols()
	if len(act["US.BBB"]) != 2 || len(act["US.AAA"]) != 0 {
		t.Fatalf("ActiveSymbols = %v, want BBB in, AAA pressure-evicted", act)
	}
	var unsubs int
	for _, c := range rpc.snapshot() {
		if !c.GetIsSubOrUnSub() {
			unsubs++
		}
	}
	if unsubs != 1 {
		t.Fatalf("unsubscribe calls = %d, want exactly 1 (pressure eviction)", unsubs)
	}
}

func TestBudgetStarvesLRUNonFocused(t *testing.T) {
	m, _, clk := newTestManager(t, 5) // room for one watch(2) + one focused(4)? no: 5 slots
	m.Ensure(feed.WatchDemand("w-old", "US.OLD")) // 2 slots, oldest
	clk.Advance(time.Second)
	m.Ensure(feed.FocusedDemand("f", "US.FOC")) // 4 slots, focused
	m.pass(context.Background())
	// Focused first (4 slots), then LRU: OLD needs 2 > remaining 1 → starved.
	if got := m.Slots(); got != 4 {
		t.Fatalf("Slots = %d, want 4 (focused only)", got)
	}
	if s := m.Starved(); len(s) != 1 || s[0] != "US.OLD" {
		t.Fatalf("Starved = %v, want [US.OLD]", s)
	}
	// Freeing the focused demand lets the starved symbol subscribe next pass.
	m.Release("f")
	clk.Advance(10 * time.Minute)
	m.pass(context.Background())
	if s := m.Starved(); len(s) != 0 {
		t.Fatalf("Starved after release = %v, want none", s)
	}
}

func TestResubscribeAllReissuesActiveSet(t *testing.T) {
	m, rpc, _ := newTestManager(t, 100)
	m.Ensure(feed.WatchDemand("w", "US.AAPL"))
	m.Ensure(feed.FocusedDemand("f", "US.TSLA"))
	m.pass(context.Background())
	before := len(rpc.snapshot())
	if err := m.ResubscribeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := rpc.snapshot()[before:]
	if len(after) != 2 { // two subtype-groups: [Ticker,KL1m] and [Quote,Book,Ticker,KL1m]
		t.Fatalf("ResubscribeAll issued %d calls, want 2 (one per subtype-group)", len(after))
	}
	act := m.ActiveSymbols()
	if len(act) != 2 || len(act["US.TSLA"]) != 4 {
		t.Fatalf("ActiveSymbols = %v", act)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/feed/opend/ -run 'TestEnsure|TestUnsubscribe|TestBudget|TestResubscribe' -v`
Expected: FAIL — `newSubManager` undefined.

- [ ] **Step 3: Implement**

`engine/internal/feed/opend/subman.go`. Note the test seam: the worker loop calls the same `pass(ctx)` method the tests call synchronously.

```go
package opend

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

// rpc is the request seam (satisfied by *Client) so the manager and backfill
// are testable without a socket.
type rpc interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error)
}

func pbSubType(s feed.SubType) int32 {
	switch s {
	case feed.SubQuote:
		return int32(qotcommon.SubType_SubType_Basic) // 1
	case feed.SubBook:
		return int32(qotcommon.SubType_SubType_OrderBook) // 2
	case feed.SubTicker:
		return int32(qotcommon.SubType_SubType_Ticker) // 4
	case feed.SubKL1m:
		return int32(qotcommon.SubType_SubType_KL_1Min) // 11
	}
	return 0
}

type subOptions struct {
	Budget       int
	MinHold      time.Duration
	Hysteresis   time.Duration
	ExtendedTime bool
}

type subKey struct {
	Symbol string
	Sub    feed.SubType
}

type subState struct {
	subscribedAt time.Time
	droppedAt    time.Time // zero while still desired
}

type demandState struct {
	d          feed.Demand
	lastEnsure time.Time
}

// subManager owns the moomoo subscription quota: it is the ONLY component
// that issues Qot_Sub. Consumers declare demands; live subscriptions are the
// union of demands, capped by the slot budget (focused symbols first, then
// most-recently-demanded). Unsubscribes are delayed by MinHold (moomoo's 60 s
// rule) and Hysteresis (symbol-flipping must not churn quota).
type subManager struct {
	rpc rpc
	clk clock.Clock
	opt subOptions

	mu      sync.Mutex
	demands map[string]*demandState
	active  map[subKey]*subState
	starved map[string]bool
	kick    chan struct{}
}

func newSubManager(r rpc, clk clock.Clock, o subOptions) *subManager {
	if o.Budget == 0 {
		o.Budget = 100
	}
	if o.MinHold == 0 {
		o.MinHold = time.Minute
	}
	if o.Hysteresis == 0 {
		o.Hysteresis = 5 * time.Minute
	}
	return &subManager{
		rpc: r, clk: clk, opt: o,
		demands: make(map[string]*demandState),
		active:  make(map[subKey]*subState),
		starved: make(map[string]bool),
		kick:    make(chan struct{}, 1),
	}
}

// Run is the worker loop: reconcile on every kick and once per second (so
// hysteresis deadlines fire without kicks).
func (m *subManager) Run(ctx context.Context) {
	tick := m.clk.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
		case <-tick.C():
		}
		m.pass(ctx)
	}
}

func (m *subManager) Ensure(d feed.Demand) {
	m.mu.Lock()
	m.demands[d.ID] = &demandState{d: d, lastEnsure: m.clk.Now()}
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) Release(id string) {
	m.mu.Lock()
	delete(m.demands, id)
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) kickWorker() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// desired computes the target set capped to capSlots (the budget minus
// slots pinned by moomoo's min-hold rule). Caller holds m.mu.
func (m *subManager) desired(capSlots int) (map[subKey]bool, []string) {
	type symDemand struct {
		symbol  string
		focused bool
		latest  time.Time
		subs    map[feed.SubType]bool
	}
	bySym := make(map[string]*symDemand)
	for _, ds := range m.demands {
		sd := bySym[ds.d.Symbol]
		if sd == nil {
			sd = &symDemand{symbol: ds.d.Symbol, subs: make(map[feed.SubType]bool)}
			bySym[ds.d.Symbol] = sd
		}
		for _, s := range ds.d.Subs {
			sd.subs[s] = true
		}
		sd.focused = sd.focused || ds.d.Focused
		if ds.lastEnsure.After(sd.latest) {
			sd.latest = ds.lastEnsure
		}
	}
	order := make([]*symDemand, 0, len(bySym))
	for _, sd := range bySym {
		order = append(order, sd)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].focused != order[j].focused {
			return order[i].focused
		}
		if !order[i].latest.Equal(order[j].latest) {
			return order[i].latest.After(order[j].latest)
		}
		return order[i].symbol < order[j].symbol // deterministic tiebreak
	})
	want := make(map[subKey]bool)
	var starved []string
	used := 0
	for _, sd := range order {
		if used+len(sd.subs) > capSlots {
			starved = append(starved, sd.symbol)
			continue
		}
		used += len(sd.subs)
		for s := range sd.subs {
			want[subKey{Symbol: sd.symbol, Sub: s}] = true
		}
	}
	sort.Strings(starved)
	return want, starved
}

// pass reconciles active against desired. Subscribes are batched per
// subtype-group; unsubscribes wait for MinHold+Hysteresis — but hysteresis
// holds a slot only while it's free: under budget pressure, lingering slots
// past MinHold are evicted (oldest droppedAt first, per-slot) to make room.
// MinHold is never waived (moomoo rejects early unsubscribes); demands that
// can't fit while slots are pinned stay starved and retry as holds expire.
// Exposed as a method (not inlined in Run) so tests drive passes synchronously.
func (m *subManager) pass(ctx context.Context) {
	now := m.clk.Now()

	m.mu.Lock()
	// Rule 2: stamp droppedAt on the first pass that sees an entry undesired.
	rawWant := make(map[subKey]bool)
	for _, ds := range m.demands {
		for _, s := range ds.d.Subs {
			rawWant[subKey{Symbol: ds.d.Symbol, Sub: s}] = true
		}
	}
	pinned := 0 // undesired but inside MinHold: nothing can free these yet
	for k, st := range m.active {
		if rawWant[k] {
			st.droppedAt = time.Time{} // re-desired: cancel pending unsubscribe
			continue
		}
		if st.droppedAt.IsZero() {
			st.droppedAt = now
		}
		if now.Sub(st.subscribedAt) < m.opt.MinHold {
			pinned++
		}
	}

	want, starved := m.desired(m.opt.Budget - pinned)
	newStarved := make(map[string]bool, len(starved))
	for _, s := range starved { // log starvation transitions once per change
		newStarved[s] = true
		if !m.starved[s] {
			slog.Warn("subscription quota pressure: symbol starved", "symbol", s, "budget", m.opt.Budget)
		}
	}
	m.starved = newStarved

	var adds []subKey
	for k := range want {
		if _, ok := m.active[k]; !ok {
			adds = append(adds, k)
		}
	}
	var removes []subKey
	removed := make(map[subKey]bool)
	for k, st := range m.active {
		if want[k] || st.droppedAt.IsZero() {
			continue
		}
		if now.Sub(st.subscribedAt) >= m.opt.MinHold && now.Sub(st.droppedAt) >= m.opt.Hysteresis {
			removes = append(removes, k)
			removed[k] = true
		}
	}
	// Rule 3: pressure eviction — free enough hysteresis-held slots for adds.
	if projected := len(m.active) - len(removes) + len(adds); projected > m.opt.Budget {
		var lingering []subKey
		for k, st := range m.active {
			if want[k] || removed[k] || st.droppedAt.IsZero() {
				continue
			}
			if now.Sub(st.subscribedAt) >= m.opt.MinHold {
				lingering = append(lingering, k)
			}
		}
		sort.Slice(lingering, func(i, j int) bool {
			a, b := m.active[lingering[i]], m.active[lingering[j]]
			if !a.droppedAt.Equal(b.droppedAt) {
				return a.droppedAt.Before(b.droppedAt)
			}
			if lingering[i].Symbol != lingering[j].Symbol { // deterministic
				return lingering[i].Symbol < lingering[j].Symbol
			}
			return lingering[i].Sub < lingering[j].Sub
		})
		for _, k := range lingering {
			if projected <= m.opt.Budget {
				break
			}
			removes = append(removes, k)
			removed[k] = true
			projected--
		}
	}
	m.mu.Unlock()

	for _, group := range groupBySubTypeSet(adds) {
		if err := m.qotSub(ctx, group.symbols, group.subs, true); err != nil {
			slog.Warn("subscribe failed; will retry next pass", "symbols", group.symbols, "err", err)
			continue
		}
		m.mu.Lock()
		for _, k := range group.keys {
			m.active[k] = &subState{subscribedAt: now}
		}
		m.mu.Unlock()
	}
	for _, group := range groupBySubTypeSet(removes) {
		if err := m.qotSub(ctx, group.symbols, group.subs, false); err != nil {
			slog.Warn("unsubscribe failed; will retry next pass", "symbols", group.symbols, "err", err)
			continue
		}
		m.mu.Lock()
		for _, k := range group.keys {
			delete(m.active, k)
		}
		m.mu.Unlock()
	}
}

// subGroup is a set of symbols sharing an identical subtype set — Qot_Sub
// subscribes the cross product SecurityList x SubTypeList, so only symbols
// with the same subtype set can share a call.
type subGroup struct {
	symbols []string
	subs    []feed.SubType
	keys    []subKey
}

func groupBySubTypeSet(keys []subKey) []subGroup {
	bySym := make(map[string][]feed.SubType)
	for _, k := range keys {
		bySym[k.Symbol] = append(bySym[k.Symbol], k.Sub)
	}
	bySig := make(map[string]*subGroup)
	for sym, subs := range bySym {
		sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
		var sig strings.Builder
		for _, s := range subs {
			fmt.Fprintf(&sig, "%d,", s)
		}
		g, ok := bySig[sig.String()]
		if !ok {
			g = &subGroup{subs: subs}
			bySig[sig.String()] = g
		}
		g.symbols = append(g.symbols, sym)
		for _, s := range subs {
			g.keys = append(g.keys, subKey{Symbol: sym, Sub: s})
		}
	}
	out := make([]subGroup, 0, len(bySig))
	for _, g := range bySig {
		sort.Strings(g.symbols) // deterministic call contents
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].symbols[0] < out[j].symbols[0] })
	return out
}

func (m *subManager) qotSub(ctx context.Context, symbols []string, subs []feed.SubType, subscribe bool) error {
	secs := make([]*qotcommon.Security, 0, len(symbols))
	for _, s := range symbols {
		sec, err := parseSymbol(s)
		if err != nil {
			return err
		}
		secs = append(secs, sec)
	}
	subTypes := make([]int32, 0, len(subs))
	for _, s := range subs {
		subTypes = append(subTypes, pbSubType(s))
	}
	// RegPushRehabTypeList is deliberately unset — the official Python SDK
	// never sets it either (quote_query.py pack_sub_or_unsub_req, verified
	// 2026-07-05) and K_1M pushes flow fine (2026-07-03 benchmark).
	req := &qotsub.Request{C2S: &qotsub.C2S{
		SecurityList:     secs,
		SubTypeList:      subTypes,
		IsSubOrUnSub:     proto.Bool(subscribe),
		IsRegOrUnRegPush: proto.Bool(subscribe),
		IsFirstPush:      proto.Bool(subscribe),
		ExtendedTime:     proto.Bool(m.opt.ExtendedTime),
	}}
	f, err := m.rpc.Request(ctx, ProtoQotSub, req)
	if err != nil {
		return err
	}
	var resp qotsub.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("qot_sub decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return fmt.Errorf("qot_sub retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
	}
	return nil
}

// ResubscribeAll reissues the full active set (reconnect path) and refreshes
// subscribedAt so MinHold restarts on the new session.
func (m *subManager) ResubscribeAll(ctx context.Context) error {
	m.mu.Lock()
	keys := make([]subKey, 0, len(m.active))
	for k := range m.active {
		keys = append(keys, k)
	}
	m.mu.Unlock()
	now := m.clk.Now()
	for _, group := range groupBySubTypeSet(keys) {
		if err := m.qotSub(ctx, group.symbols, group.subs, true); err != nil {
			return err
		}
	}
	m.mu.Lock()
	for _, st := range m.active {
		st.subscribedAt = now
	}
	m.mu.Unlock()
	return nil
}

// ActiveSymbols returns the live subscription map (symbol → subtypes),
// used by the reconnect reseed.
func (m *subManager) ActiveSymbols() map[string][]feed.SubType {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]feed.SubType)
	for k := range m.active {
		out[k.Symbol] = append(out[k.Symbol], k.Sub)
	}
	for _, subs := range out {
		sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
	}
	return out
}

func (m *subManager) Slots() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func (m *subManager) Starved() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.starved))
	for s := range m.starved {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
```

Add `"sync"` to the imports.

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/opend/subman.go internal/feed/opend/subman_test.go
git commit -m "feat(engine/opend): subscription manager - refcounted demands, batching, min-hold, hysteresis, budget"
```

## Task 7: Backfill — cache reads, history K-line, quota guard

The benchmarked cheap paths (quota-free, local-cache reads) plus the only quota-spending call, `Qot_RequestHistoryKL`, behind a guard.

**Files:**
- Create: `engine/internal/feed/opend/backfill.go`
- Test: `engine/internal/feed/opend/backfill_test.go`
- Modify: `engine/internal/feed/opend/mock_opend_test.go` (qot request handlers used by this task's and Task 8's tests)

**Interfaces:**
- Consumes: `rpc` seam (Task 6), decoders (Task 5), pb packages `qotgetkl`, `qotgetticker`, `qotgetorderbook`, `qotgetbasicqot`, `qotrequesthistorykl`, `qotrequesthistoryklquota`.
- Produces (used by Task 8):

```go
func newBackfill(r rpc) *backfill
func (b *backfill) cachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error)   // Qot_GetKL 3006, forward-adjusted, n≤1000, ~9ms, quota-free
func (b *backfill) recentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error)   // Qot_GetTicker 3010, n≤1000, ~30ms, quota-free
func (b *backfill) bookSnapshot(ctx context.Context, symbol string) (feed.Book, error)           // Qot_GetOrderBook 3012, 10 levels, ~2.5ms
func (b *backfill) quoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error)         // Qot_GetBasicQot 3004
func (b *backfill) historyBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) // Qot_RequestHistoryKL 3103, paginated
func (b *backfill) historyQuota(ctx context.Context) (used, remain int, err error)               // Qot_RequestHistoryKLQuota 3104
var ErrHistoryQuotaExhausted = errors.New("opend: history K-line quota exhausted")
```

**Semantics:**
- All requests clamp `n` to 1,000 (API max).
- `historyBars` pages with `MaxAckKLNum=1000` and round-trips `NextReqKey` until empty, capped at 40 pages (40k bars — a runaway-pagination backstop, logged if hit). `RehabType=Forward` always. `ExtendedTime=true` for `Res1m` only (the API supports it only ≤60m). `BeginTime`/`EndTime` format `"2006-01-02 15:04:05"` rendered in ET (`session.Loc()`) for `Res1m`, `"2006-01-02"` for `ResDay`.
- Every response is `RetType`-checked; failures return `fmt.Errorf("opend: proto %d: retType=%d msg=%q", ...)`-style errors.
- The quota *guard* lives in Task 8 (`OpenDFeed.HistoryBars`) because it needs the fetched-symbols memory; this task only exposes `historyQuota`.

- [ ] **Step 1: Extend the mock with qot request handlers**

In `engine/internal/feed/opend/mock_opend_test.go`, add canned qot data plus the helpers Tasks 7–8 need (tests can still override `m.handler` entirely). Add fields to `mockOpenD`: `data map[string]*qotData`, `conns []net.Conn` (recorded in `handleConn`, removed on close), and `quotaRemain int` (initialize to 97 in `newMockOpenD`); all guarded by the existing `mu`.

```go
// qotData lets tests preload canned cache/history responses per symbol
// (key = the "US.AAPL"-form symbol, i.e. formatSymbol of the request).
type qotData struct {
	bars1m  []*qotcommon.KLine // served by Qot_GetKL and (paged) Qot_RequestHistoryKL
	ticks   []*qotcommon.Ticker
	bids    []*qotcommon.OrderBook
	asks    []*qotcommon.OrderBook
	basic   *qotcommon.BasicQot
	pageLen int // history page size; 0 = everything in one page
}

func (m *mockOpenD) setData(symbol string, d *qotData) {
	m.mu.Lock()
	if m.data == nil {
		m.data = make(map[string]*qotData)
	}
	m.data[symbol] = d
	m.mu.Unlock()
}

func (m *mockOpenD) dataFor(sec *qotcommon.Security) *qotData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.data[formatSymbol(sec)]; d != nil {
		return d
	}
	return &qotData{}
}

func (m *mockOpenD) snapshotRequests() []Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Frame(nil), m.requests...)
}

// pushToAll sends a push frame on every live connection.
func (m *mockOpenD) pushToAll(protoID, serialNo uint32, msg proto.Message) {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.mu.Unlock()
	for _, c := range conns {
		m.push(c, protoID, serialNo, msg)
	}
}

// dropAllConns severs every live connection (reconnect tests).
func (m *mockOpenD) dropAllConns() {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}
```

Extend `defaultHandler` with the qot cases — dumb canned replies, no time-range filtering (tests assert on `m.snapshotRequests()`):

```go
	case ProtoQotSub:
		m.reply(conn, f, &qotsub.Response{RetType: proto.Int32(0), S2C: &qotsub.S2C{}})
	case ProtoQotGetKL:
		var req qotgetkl.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetkl.Response{RetType: proto.Int32(0),
			S2C: &qotgetkl.S2C{Security: req.GetC2S().GetSecurity(), KlList: d.bars1m}})
	case ProtoQotGetTicker:
		var req qotgetticker.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetticker.Response{RetType: proto.Int32(0),
			S2C: &qotgetticker.S2C{Security: req.GetC2S().GetSecurity(), TickerList: d.ticks}})
	case ProtoQotGetOrderBook:
		var req qotgetorderbook.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetorderbook.Response{RetType: proto.Int32(0),
			S2C: &qotgetorderbook.S2C{Security: req.GetC2S().GetSecurity(),
				OrderBookBidList: d.bids, OrderBookAskList: d.asks}})
	case ProtoQotGetBasicQot:
		var req qotgetbasicqot.Request
		_ = proto.Unmarshal(f.Body, &req)
		var list []*qotcommon.BasicQot
		if len(req.GetC2S().GetSecurityList()) > 0 {
			if d := m.dataFor(req.GetC2S().GetSecurityList()[0]); d.basic != nil {
				list = append(list, d.basic)
			}
		}
		m.reply(conn, f, &qotgetbasicqot.Response{RetType: proto.Int32(0),
			S2C: &qotgetbasicqot.S2C{BasicQotList: list}})
	case ProtoQotRequestHistoryKL:
		var req qotrequesthistorykl.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		start := 0
		if key := req.GetC2S().GetNextReqKey(); len(key) == 1 { // offset byte
			start = int(key[0])
		}
		page := d.bars1m[start:]
		var next []byte
		if d.pageLen > 0 && len(page) > d.pageLen {
			page = page[:d.pageLen]
			next = []byte{byte(start + d.pageLen)}
		}
		m.reply(conn, f, &qotrequesthistorykl.Response{RetType: proto.Int32(0),
			S2C: &qotrequesthistorykl.S2C{Security: req.GetC2S().GetSecurity(),
				KlList: page, NextReqKey: next}})
	case ProtoQotRequestHistoryKLQuota:
		m.mu.Lock()
		remain := m.quotaRemain
		m.mu.Unlock()
		m.reply(conn, f, &qotrequesthistoryklquota.Response{RetType: proto.Int32(0),
			S2C: &qotrequesthistoryklquota.S2C{
				UsedQuota: proto.Int32(int32(100 - remain)), RemainQuota: proto.Int32(int32(remain))}})
```

Add the new pb imports (`qotsub`, `qotgetkl`, `qotgetticker`, `qotgetorderbook`, `qotgetbasicqot`, `qotrequesthistorykl`, `qotrequesthistoryklquota`, `qotcommon`) to the mock file.

- [ ] **Step 2: Write the failing tests**

`engine/internal/feed/opend/backfill_test.go`:

```go
package opend

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
)

func kl(tsSec float64, c float64, v int64) *qotcommon.KLine {
	return &qotcommon.KLine{
		Timestamp: proto.Float64(tsSec), OpenPrice: proto.Float64(c), HighPrice: proto.Float64(c),
		LowPrice: proto.Float64(c), ClosePrice: proto.Float64(c), Volume: proto.Int64(v),
	}
}

// liveClient dials the mock and returns a connected client (helper shared
// with Task 8's tests; reuse the ConnUp-waiting pattern from client_test.go).
func liveClient(t *testing.T, m *mockOpenD) *Client {
	t.Helper()
	c := New(Options{Addr: m.addr()})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	waitForState(t, c, ConnUp)
	return c
}

func TestCachedBars1mDecodesAndClamps(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{
		kl(1782146460, 309.1, 1000), // end-labeled: bucket = timestamp − 60 s
		kl(1782146520, 309.2, 1100),
	}})
	b := newBackfill(liveClient(t, m))
	bars, err := b.cachedBars1m(context.Background(), "US.AAPL", 5000) // clamped to 1000
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars[0].BucketMs != 1782146460_000-60_000 {
		t.Fatalf("bars = %+v", bars)
	}
}

func TestHistoryBarsPaginates(t *testing.T) {
	m := newMockOpenD(t)
	var kls []*qotcommon.KLine
	for i := 0; i < 5; i++ {
		kls = append(kls, kl(1782146460+float64(60*i), 300+float64(i), 100))
	}
	m.setData("US.AAPL", &qotData{bars1m: kls, pageLen: 2}) // 3 pages: 2+2+1
	b := newBackfill(liveClient(t, m))
	from := time.UnixMilli(1782140000_000)
	to := time.UnixMilli(1782150000_000)
	bars, err := b.historyBars(context.Background(), "US.AAPL", feed.Res1m, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 5 {
		t.Fatalf("got %d bars across pages, want 5", len(bars))
	}
	// Three history requests were made, page 2 and 3 carrying NextReqKey.
	var histReqs []*qotrequesthistorykl.C2S
	for _, f := range m.snapshotRequests() {
		if f.ProtoID == ProtoQotRequestHistoryKL {
			var r qotrequesthistorykl.Request
			if err := proto.Unmarshal(f.Body, &r); err != nil {
				t.Fatal(err)
			}
			histReqs = append(histReqs, r.GetC2S())
		}
	}
	if len(histReqs) != 3 {
		t.Fatalf("history requests = %d, want 3", len(histReqs))
	}
	if histReqs[0].GetNextReqKey() != nil || histReqs[1].GetNextReqKey() == nil {
		t.Fatal("NextReqKey must be absent on page 1, present on page 2")
	}
	if !histReqs[0].GetExtendedTime() {
		t.Fatal("Res1m history must set ExtendedTime")
	}
	if got := qotcommon.RehabType(histReqs[0].GetRehabType()); got != qotcommon.RehabType_RehabType_Forward {
		t.Fatalf("RehabType = %v, want Forward", got)
	}
}

func TestHistoryQuota(t *testing.T) {
	m := newMockOpenD(t)
	b := newBackfill(liveClient(t, m))
	used, remain, err := b.historyQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used != 3 || remain != 97 {
		t.Fatalf("quota = (%d,%d), want (3,97)", used, remain)
	}
}
```

Add a `snapshotRequests()` accessor on the mock (mu-guarded copy of `m.requests`) if Plan 1 didn't already expose one.

- [ ] **Step 3: Run to verify failure, then implement**

Run: `cd engine && go test ./internal/feed/opend/ -run 'TestCachedBars|TestHistory' -v` → FAIL (`newBackfill` undefined).

`engine/internal/feed/opend/backfill.go`:

```go
package opend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetbasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetticker"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// ErrHistoryQuotaExhausted is returned when a deep backfill would spend a
// history slot the account no longer has. Charts degrade to the quota-free
// cache (≤1,000 1m bars) with a logged warning — never silently.
var ErrHistoryQuotaExhausted = errors.New("opend: history K-line quota exhausted")

// maxAPIRows is moomoo's per-call cap for cache reads and history pages.
const maxAPIRows = 1000

// maxHistoryPages caps pagination (40k bars) as a runaway backstop.
const maxHistoryPages = 40

// backfill wraps the benchmarked cheap read paths: get_cur_kline ~9 ms,
// get_rt_ticker ~30 ms, get_order_book ~2.5 ms — all quota-free local-cache
// reads — plus the quota-spending Qot_RequestHistoryKL.
type backfill struct {
	rpc rpc
}

func newBackfill(r rpc) *backfill { return &backfill{rpc: r} }

func clampRows(n int) int32 {
	if n <= 0 || n > maxAPIRows {
		return maxAPIRows
	}
	return int32(n)
}

func retErr(protoID uint32, retType int32, msg string) error {
	return fmt.Errorf("opend: proto %d: retType=%d msg=%q", protoID, retType, msg)
}

func (b *backfill) cachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	req := &qotgetkl.Request{C2S: &qotgetkl.C2S{
		RehabType: proto.Int32(int32(qotcommon.RehabType_RehabType_Forward)),
		KlType:    proto.Int32(int32(qotcommon.KLType_KLType_1Min)),
		Security:  sec,
		ReqNum:    proto.Int32(clampRows(n)),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetKL, req)
	if err != nil {
		return nil, err
	}
	var resp qotgetkl.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return nil, fmt.Errorf("get_kl decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return nil, retErr(ProtoQotGetKL, resp.GetRetType(), resp.GetRetMsg())
	}
	return decodeKLines(symbol, resp.GetS2C().GetKlList(), feed.Res1m)
}

func decodeKLines(symbol string, list []*qotcommon.KLine, res feed.Resolution) ([]feed.Bar, error) {
	bars := make([]feed.Bar, 0, len(list))
	for _, k := range list {
		bar, err := decodeKLine(symbol, k, res)
		if err != nil {
			return nil, err
		}
		bars = append(bars, bar)
	}
	return bars, nil
}

func (b *backfill) recentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	req := &qotgetticker.Request{C2S: &qotgetticker.C2S{
		Security:  sec,
		MaxRetNum: proto.Int32(clampRows(n)),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetTicker, req)
	if err != nil {
		return nil, err
	}
	var resp qotgetticker.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return nil, fmt.Errorf("get_ticker decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return nil, retErr(ProtoQotGetTicker, resp.GetRetType(), resp.GetRetMsg())
	}
	ticks := make([]feed.Tick, 0, len(resp.GetS2C().GetTickerList()))
	for _, t := range resp.GetS2C().GetTickerList() {
		ticks = append(ticks, decodeTicker(symbol, t))
	}
	return ticks, nil
}

func (b *backfill) bookSnapshot(ctx context.Context, symbol string) (feed.Book, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.Book{}, err
	}
	req := &qotgetorderbook.Request{C2S: &qotgetorderbook.C2S{
		Security: sec,
		Num:      proto.Int32(10), // API max for securities; entitlement-gated
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetOrderBook, req)
	if err != nil {
		return feed.Book{}, err
	}
	var resp qotgetorderbook.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return feed.Book{}, fmt.Errorf("get_order_book decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return feed.Book{}, retErr(ProtoQotGetOrderBook, resp.GetRetType(), resp.GetRetMsg())
	}
	s2c := resp.GetS2C()
	return feed.Book{
		Symbol: symbol,
		TsMs:   tsMs(max(s2c.GetSvrRecvTimeBidTimestamp(), s2c.GetSvrRecvTimeAskTimestamp())),
		Bids:   decodeBookLevels(s2c.GetOrderBookBidList()),
		Asks:   decodeBookLevels(s2c.GetOrderBookAskList()),
	}, nil
}

func (b *backfill) quoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.Quote{}, err
	}
	req := &qotgetbasicqot.Request{C2S: &qotgetbasicqot.C2S{
		SecurityList: []*qotcommon.Security{sec},
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetBasicQot, req)
	if err != nil {
		return feed.Quote{}, err
	}
	var resp qotgetbasicqot.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return feed.Quote{}, fmt.Errorf("get_basic_qot decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return feed.Quote{}, retErr(ProtoQotGetBasicQot, resp.GetRetType(), resp.GetRetMsg())
	}
	list := resp.GetS2C().GetBasicQotList()
	if len(list) == 0 {
		return feed.Quote{}, fmt.Errorf("opend: no basic quote returned for %s", symbol)
	}
	return decodeBasicQot(list[0])
}

// historyBars pulls deep history through the quota-tracked API, paging via
// NextReqKey. Res1m sets ExtendedTime (pre/post bars) and renders the range
// in ET with seconds; ResDay uses bare dates.
func (b *backfill) historyBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	klType, timeFmt := int32(qotcommon.KLType_KLType_Day), "2006-01-02"
	extended := false
	if res == feed.Res1m {
		klType, timeFmt = int32(qotcommon.KLType_KLType_1Min), "2006-01-02 15:04:05"
		extended = true
	}
	var (
		bars    []feed.Bar
		nextKey []byte
	)
	for page := 0; page < maxHistoryPages; page++ {
		c2s := &qotrequesthistorykl.C2S{
			RehabType:   proto.Int32(int32(qotcommon.RehabType_RehabType_Forward)),
			KlType:      proto.Int32(klType),
			Security:    sec,
			BeginTime:   proto.String(from.In(session.Loc()).Format(timeFmt)),
			EndTime:     proto.String(to.In(session.Loc()).Format(timeFmt)),
			MaxAckKLNum: proto.Int32(maxAPIRows),
		}
		if extended {
			c2s.ExtendedTime = proto.Bool(true)
		}
		if nextKey != nil {
			c2s.NextReqKey = nextKey
		}
		f, err := b.rpc.Request(ctx, ProtoQotRequestHistoryKL, &qotrequesthistorykl.Request{C2S: c2s})
		if err != nil {
			return nil, err
		}
		var resp qotrequesthistorykl.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("request_history_kl decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, retErr(ProtoQotRequestHistoryKL, resp.GetRetType(), resp.GetRetMsg())
		}
		pageBars, err := decodeKLines(symbol, resp.GetS2C().GetKlList(), res)
		if err != nil {
			return nil, err
		}
		bars = append(bars, pageBars...)
		nextKey = resp.GetS2C().GetNextReqKey()
		if len(nextKey) == 0 {
			return bars, nil
		}
	}
	slog.Warn("history pagination hit the page cap; result truncated",
		"symbol", symbol, "pages", maxHistoryPages, "bars", len(bars))
	return bars, nil
}

func (b *backfill) historyQuota(ctx context.Context) (used, remain int, err error) {
	req := &qotrequesthistoryklquota.Request{C2S: &qotrequesthistoryklquota.C2S{
		BGetDetail: proto.Bool(false),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotRequestHistoryKLQuota, req)
	if err != nil {
		return 0, 0, err
	}
	var resp qotrequesthistoryklquota.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return 0, 0, fmt.Errorf("history_quota decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return 0, 0, retErr(ProtoQotRequestHistoryKLQuota, resp.GetRetType(), resp.GetRetMsg())
	}
	return int(resp.GetS2C().GetUsedQuota()), int(resp.GetS2C().GetRemainQuota()), nil
}
```

(Go 1.21+ has builtin `max` for float64.)

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/opend/backfill.go internal/feed/opend/backfill_test.go internal/feed/opend/mock_opend_test.go
git commit -m "feat(engine/opend): backfill reads - kline/tick/book/quote caches + paginated history + quota query"
```

---

## Task 8: `OpenDFeed` — the `feed.Feed` implementation

Composes client + subManager + backfill into the adapter: pushes become events, `Ensure` auto-seeds from the caches, reconnects re-subscribe → re-seed → `Resynced`.

**Files:**
- Create: `engine/internal/feed/opend/opendfeed.go`
- Test: `engine/internal/feed/opend/opendfeed_test.go`

**Interfaces:**
- Consumes: `*Client` (`Pushes`, `State`, `Request`), `subManager`, `backfill`, `DecodePush`, `clock.Clock`, `feed.*`.
- Produces (used by Task 14 and Plan 3):

```go
type FeedOptions struct {
	Budget              int           // → subManager
	Hysteresis          time.Duration // → subManager
	DisableExtendedTime bool          // inverted: zero value = extended hours ON
	EventBuf            int           // Events() capacity, default 4096
	Clock               clock.Clock   // default clock.System{}
}
func NewOpenDFeed(cli *Client, opt FeedOptions) *OpenDFeed  // implements feed.Feed
func (f *OpenDFeed) Run(ctx context.Context) error          // blocks; own goroutine
```

**Semantics (encode exactly):**
1. **Pump:** one goroutine drains `cli.Pushes()`; `DecodePush` errors increment a counter and log every 100th occurrence (never spam, never hide); events go to the events channel with a **blocking** send — the md core is the only consumer and is fast; upstream (client push buffer, cap 1024) drops with its own accounting if the whole pipeline wedges.
2. **State loop:** one goroutine drains `cli.State()`. `ConnDown` → emit `ConnDownEvent`. `ConnUp` → emit `ConnUpEvent`, then `sub.ResubscribeAll(ctx)`, then re-seed every symbol in `sub.ActiveSymbols()` (inline, serially — ordering matters), then emit `ResyncedEvent`. Seed failures log and continue (partial reseed beats none; honesty via log + counter).
3. **Seeding rules per subtype** (both initial `Ensure` and reconnect): `SubKL1m` → `cachedBars1m(1000)` → `Bars1mEvent{Seed:true}`; `SubTicker` → `recentTicks(1000)` → `TicksEvent{Seed:true}`; `SubBook` → `bookSnapshot` → `BookEvent{Seed:true}`; `SubQuote` → `quoteSnapshot` → `QuoteEvent{Seed:true}`. Ordering per symbol: bars, ticks, book, quote.
4. **Ensure:** delegates to `sub.Ensure`, then enqueues a seed job (buffered channel, one worker goroutine) for the demand's symbol+subs. The demand's data starts flowing without the caller blocking. `Release` just delegates.
5. **Queries:** `HistoryBars` guards quota: an in-memory `fetched map[string]time.Time` records symbols fetched in the last 30 days (the moomoo dedup window — re-requests are free); an unfetched symbol first checks `historyQuota` and returns `ErrHistoryQuotaExhausted` when `remain == 0`. Other queries delegate straight to `backfill`.

- [ ] **Step 1: Write the failing integration tests**

`engine/internal/feed/opend/opendfeed_test.go`:

```go
package opend

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

// nextEvent pulls one event or fails the test on timeout.
func nextEvent(t *testing.T, ch <-chan feed.Event) feed.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for feed event")
		return nil
	}
}

func TestEnsureSubscribesAndSeeds(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{
		bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)},
		ticks: []*qotcommon.Ticker{{
			Sequence: proto.Int64(1), Timestamp: proto.Float64(1782146400.5),
			Price: proto.Float64(309), Volume: proto.Int64(100),
			Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid)),
		}},
	})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.WatchDemand("w", "US.AAPL"))

	// Watch profile seeds bars then ticks (per-subtype order: KL, Ticker).
	if ev, ok := nextEvent(t, f.Events()).(feed.Bars1mEvent); !ok || !ev.Seed || len(ev.Bars) != 1 {
		t.Fatalf("first event = %#v, want seed Bars1mEvent", ev)
	}
	if ev, ok := nextEvent(t, f.Events()).(feed.TicksEvent); !ok || !ev.Seed || ev.Ticks[0].Seq != 1 {
		t.Fatalf("second event = %#v, want seed TicksEvent", ev)
	}

	// A live push now flows through as a non-seed event.
	m.pushToAll(ProtoQotUpdateTicker, 1517, &qotupdateticker.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateticker.S2C{
			Security: sec(11, "AAPL"),
			TickerList: []*qotcommon.Ticker{{
				Sequence: proto.Int64(2), Timestamp: proto.Float64(1782146401.0),
				Price: proto.Float64(309.05), Volume: proto.Int64(50),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Ask)),
			}},
		},
	})
	if ev, ok := nextEvent(t, f.Events()).(feed.TicksEvent); !ok || ev.Seed || ev.Ticks[0].Seq != 2 {
		t.Fatalf("push event = %#v, want live TicksEvent seq=2", ev)
	}
}

func TestReconnectResubscribesReseedsAndEmitsResynced(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)}})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.Demand{ID: "d", Symbol: "US.AAPL", Subs: []feed.SubType{feed.SubKL1m}})
	nextEvent(t, f.Events()) // initial seed

	subsBefore := countQotSubs(m)
	m.dropAllConns() // sever: client reconnects via backoff

	// Expect, in order: ConnDown, ConnUp, seed Bars1m (reseed), Resynced.
	var seen []string
	for len(seen) < 4 {
		switch ev := nextEvent(t, f.Events()).(type) {
		case feed.ConnDownEvent:
			seen = append(seen, "down")
		case feed.ConnUpEvent:
			seen = append(seen, "up")
		case feed.Bars1mEvent:
			if !ev.Seed {
				continue
			}
			seen = append(seen, "seed")
		case feed.ResyncedEvent:
			seen = append(seen, "resynced")
		}
	}
	want := []string{"down", "up", "seed", "resynced"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("event order = %v, want %v", seen, want)
		}
	}
	if countQotSubs(m) <= subsBefore {
		t.Fatal("no re-subscribe Qot_Sub after reconnect")
	}
}

func countQotSubs(m *mockOpenD) int {
	n := 0
	for _, f := range m.snapshotRequests() {
		if f.ProtoID == ProtoQotSub {
			n++
		}
	}
	return n
}

func TestHistoryBarsQuotaGuard(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.NEW", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 1, 1)}})
	m.quotaRemain = 0 // make the mock report an exhausted quota
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	_, err := f.HistoryBars(ctx, "US.NEW", feed.Res1m, time.UnixMilli(0), time.UnixMilli(1))
	if !errors.Is(err, ErrHistoryQuotaExhausted) {
		t.Fatalf("err = %v, want ErrHistoryQuotaExhausted", err)
	}
}
```

Mock additions this test needs: `pushToAll(protoID, serial, msg)` (send a push on every live conn — track conns in `handleConn`), `dropAllConns()` (close them), and a `quotaRemain int` field (default 97) that the 3104 handler serves.

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/feed/opend/ -run 'TestEnsureSubscribes|TestReconnect|TestHistoryBarsQuota' -v`
Expected: FAIL — `NewOpenDFeed` undefined.

- [ ] **Step 3: Implement**

`engine/internal/feed/opend/opendfeed.go`:

```go
package opend

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// FeedOptions configures the OpenD feed adapter. Zero values get defaults —
// note DisableExtendedTime is inverted so the zero value means extended
// hours ON (eTape is a pre-market-first product).
type FeedOptions struct {
	Budget              int
	Hysteresis          time.Duration
	DisableExtendedTime bool
	EventBuf            int
	Clock               clock.Clock
}

// OpenDFeed implements feed.Feed over the low-level Client: pushes are decoded
// into events, Ensure auto-seeds from OpenD's quota-free caches, and
// reconnects re-subscribe, re-seed, and emit Resynced.
type OpenDFeed struct {
	cli *Client
	sub *subManager
	bf  *backfill
	clk clock.Clock

	events chan feed.Event
	seedq  chan seedJob

	mu          sync.Mutex
	fetched     map[string]time.Time // history-quota dedup window (30 days)
	decodeFails uint64
}

type seedJob struct {
	symbol string
	subs   []feed.SubType
}

// fetchDedupWindow mirrors moomoo's 30-day rule: re-requesting a symbol's
// history within 30 days consumes no quota, so only new symbols are guarded.
const fetchDedupWindow = 30 * 24 * time.Hour

// NewOpenDFeed wires the adapter. Call Run to start it.
func NewOpenDFeed(cli *Client, opt FeedOptions) *OpenDFeed {
	if opt.EventBuf == 0 {
		opt.EventBuf = 4096
	}
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	return &OpenDFeed{
		cli: cli,
		sub: newSubManager(cli, opt.Clock, subOptions{
			Budget:       opt.Budget,
			Hysteresis:   opt.Hysteresis,
			ExtendedTime: !opt.DisableExtendedTime,
		}),
		bf:      newBackfill(cli),
		clk:     opt.Clock,
		events:  make(chan feed.Event, opt.EventBuf),
		seedq:   make(chan seedJob, 64),
		fetched: make(map[string]time.Time),
	}
}

func (f *OpenDFeed) Events() <-chan feed.Event { return f.events }

func (f *OpenDFeed) Ensure(d feed.Demand) {
	f.sub.Ensure(d)
	select {
	case f.seedq <- seedJob{symbol: d.Symbol, subs: d.Subs}:
	default:
		slog.Warn("seed queue full; symbol will seed on next resync", "symbol", d.Symbol)
	}
}

func (f *OpenDFeed) Release(id string) { f.sub.Release(id) }

// Run blocks until ctx is done, supervising the pump, state, seed, and
// subscription-manager goroutines. The caller runs Client.Run separately.
func (f *OpenDFeed) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); f.sub.Run(ctx) }()
	go func() { defer wg.Done(); f.pump(ctx) }()
	go func() { defer wg.Done(); f.seedWorker(ctx) }()
	f.stateLoop(ctx)
	wg.Wait()
	return ctx.Err()
}

func (f *OpenDFeed) emit(ctx context.Context, ev feed.Event) {
	select {
	case f.events <- ev:
	case <-ctx.Done():
	}
}

func (f *OpenDFeed) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-f.cli.Pushes():
			evs, err := DecodePush(frame)
			if err != nil {
				f.mu.Lock()
				f.decodeFails++
				n := f.decodeFails
				f.mu.Unlock()
				if n%100 == 1 { // log the 1st, 101st, ... — visible, never spammy
					slog.Warn("push decode failure", "protoID", frame.ProtoID, "total", n, "err", err)
				}
				continue
			}
			for _, ev := range evs {
				f.emit(ctx, ev)
			}
		}
	}
}

func (f *OpenDFeed) stateLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-f.cli.State():
			switch st {
			case ConnDown:
				f.emit(ctx, feed.ConnDownEvent{})
			case ConnUp:
				f.emit(ctx, feed.ConnUpEvent{})
				if err := f.sub.ResubscribeAll(ctx); err != nil {
					slog.Error("resubscribe after reconnect failed", "err", err)
					continue // client will cycle the connection; next ConnUp retries
				}
				for symbol, subs := range f.sub.ActiveSymbols() {
					f.seed(ctx, symbol, subs)
				}
				f.emit(ctx, feed.ResyncedEvent{})
			}
		}
	}
}

func (f *OpenDFeed) seedWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-f.seedq:
			f.seed(ctx, job.symbol, job.subs)
		}
	}
}

// seed replays OpenD's local caches as Seed events, per subtype, in a fixed
// order (bars, ticks, book, quote). Failures log and continue — a partial
// seed beats none, and the md core's dedup makes overlap harmless.
func (f *OpenDFeed) seed(ctx context.Context, symbol string, subs []feed.SubType) {
	has := func(want feed.SubType) bool {
		for _, s := range subs {
			if s == want {
				return true
			}
		}
		return false
	}
	if has(feed.SubKL1m) {
		if bars, err := f.bf.cachedBars1m(ctx, symbol, maxAPIRows); err != nil {
			slog.Warn("seed bars1m failed", "symbol", symbol, "err", err)
		} else if len(bars) > 0 {
			f.emit(ctx, feed.Bars1mEvent{Bars: bars, Seed: true})
		}
	}
	if has(feed.SubTicker) {
		if ticks, err := f.bf.recentTicks(ctx, symbol, maxAPIRows); err != nil {
			slog.Warn("seed ticks failed", "symbol", symbol, "err", err)
		} else if len(ticks) > 0 {
			f.emit(ctx, feed.TicksEvent{Ticks: ticks, Seed: true})
		}
	}
	if has(feed.SubBook) {
		if book, err := f.bf.bookSnapshot(ctx, symbol); err != nil {
			slog.Warn("seed book failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.BookEvent{Book: book, Seed: true})
		}
	}
	if has(feed.SubQuote) {
		if q, err := f.bf.quoteSnapshot(ctx, symbol); err != nil {
			slog.Warn("seed quote failed", "symbol", symbol, "err", err)
		} else {
			f.emit(ctx, feed.QuoteEvent{Quote: q, Seed: true})
		}
	}
}

// HistoryBars spends history quota; guard new symbols against exhaustion.
// Symbols fetched within the 30-day dedup window are free re-requests.
func (f *OpenDFeed) HistoryBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) {
	f.mu.Lock()
	last, ok := f.fetched[symbol]
	f.mu.Unlock()
	if !ok || f.clk.Now().Sub(last) > fetchDedupWindow {
		_, remain, err := f.bf.historyQuota(ctx)
		if err != nil {
			return nil, err
		}
		if remain == 0 {
			slog.Warn("history quota exhausted; deep backfill degraded to cache depth", "symbol", symbol)
			return nil, ErrHistoryQuotaExhausted
		}
	}
	bars, err := f.bf.historyBars(ctx, symbol, res, from, to)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.fetched[symbol] = f.clk.Now()
	f.mu.Unlock()
	return bars, nil
}

func (f *OpenDFeed) RecentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error) {
	return f.bf.recentTicks(ctx, symbol, n)
}

func (f *OpenDFeed) CachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) {
	return f.bf.cachedBars1m(ctx, symbol, n)
}

func (f *OpenDFeed) BookSnapshot(ctx context.Context, symbol string) (feed.Book, error) {
	return f.bf.bookSnapshot(ctx, symbol)
}

func (f *OpenDFeed) QuoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error) {
	return f.bf.quoteSnapshot(ctx, symbol)
}

var _ feed.Feed = (*OpenDFeed)(nil)
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/feed/opend/ -v`
Expected: PASS — including reconnect ordering (`down, up, seed, resynced`).

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/feed/opend/opendfeed.go internal/feed/opend/opendfeed_test.go internal/feed/opend/mock_opend_test.go
git commit -m "feat(engine/opend): OpenDFeed - feed.Feed over client: pump, auto-seeding, resync protocol"
```

## Task 9: `md` core skeleton — single-writer loop, books, tape ring, quotes, outputs

The one goroutine that owns market-data state. No I/O, no clock, no locks: everything arrives on the inbox, everything leaves on typed channels.

**Files:**
- Create: `engine/internal/md/core.go`, `engine/internal/md/update.go`, `engine/internal/md/book.go`, `engine/internal/md/quote.go`, `engine/internal/md/tape.go`
- Test: `engine/internal/md/core_test.go`, `engine/internal/md/tape_test.go`

**Interfaces:**
- Consumes: `feed` (Task 4), `session` (Task 3). **Never** `feed/opend`.
- Produces (Tasks 10–14 build on these — signatures are binding):

```go
// update.go
type Update interface{ isUpdate() }
type QuoteUpdate struct{ Quote feed.Quote }
type BookUpdate struct{ Book feed.Book }
type TapeUpdate struct{ Symbol string; Ticks []feed.Tick }          // appended (deduped) prints
type BarUpdate struct{ Bar Bar }
type IndicatorUpdate struct {
	InstanceID string   // the requested instance
	SeriesKey  string   // instanceId (single-slot) or "instanceId#slot" (matches the UI contract)
	Points     []Point  // Snapshot: full series; else exactly one point
	Snapshot   bool
}
type MismatchUpdate struct{ Symbol string; BucketMs int64; Detail string } // K_1M vs tick-derived 1m
type ConnUpdate struct{ Up bool }
type ResyncedUpdate struct{}

type Point struct{ TimeMs int64; Value float64 }
type Mark struct{ Symbol string; Price float64; TsMs int64 }        // last-trade → exec (Plan 4)

// Bar is the md-side bar: raw OHLCV plus tick-derived delta fields and
// display state. BuyV/SellV/Ticks are zero when no tick data covers the bar
// (e.g. deep-history backfill) — the DELTA indicator reads 0 there, honestly.
type Bar struct {
	Symbol     string
	TF         session.Timeframe
	BucketMs   int64
	O, H, L, C float64
	V          int64
	BuyV       int64
	SellV      int64
	Ticks      int32
	InProgress bool
	Gap        bool // first bar after a feed gap (resync) — UI renders the flag
}

// core.go
type Config struct {
	TapeRing   int   // per-symbol tick ring capacity (default 65536)
	AnchorSecs int64 // intraday bucket anchor (default session.AnchorSecsDefault)
}
func New(cfg Config) *Core
func (c *Core) Run(ctx context.Context) error   // the single writer; blocks
func (c *Core) Feed(ev feed.Event)              // enqueue a feed event (blocking send)
func (c *Core) EnsureIndicator(id string, spec IndicatorSpec)  // Task 12
func (c *Core) ReleaseIndicator(id string)                     // Task 12
func (c *Core) SeedDaily(symbol string, bars []feed.Bar)       // official daily history
func (c *Core) SeedHistory1m(symbol string, bars []feed.Bar)   // deep intraday history
func (c *Core) Updates() <-chan Update          // cap 8192; overflow drops+counts
func (c *Core) Marks() <-chan Mark              // cap 1024; overflow drops (keep-latest semantics downstream)
func (c *Core) DroppedUpdates() uint64          // honesty counter
```

**Semantics:**
- The inbox is one channel of a small sealed message union (`feed.Event` wrapper + control messages). All exported mutators only enqueue. `Run` applies messages strictly in arrival order.
- **Tape:** fixed ring per symbol (cap `Config.TapeRing`). Dedup: a tick is dropped iff `Seq <= lastSeq[symbol]` *and* it's the same ET day (`session.DayMs(TsMs)`) — moomoo sequences restart daily. Accepted ticks append to the ring, feed the bar engine (Task 10/11), emit one `TapeUpdate` per event batch, and one `Mark` from the batch's last tick.
- **Books/quotes:** replace-latest per symbol, emit `BookUpdate`/`QuoteUpdate` (replace is cheaper than diff at ≤10 rows).
- **Conn events:** `ConnUpdate{Up}` passthrough; `ResyncedEvent` → `ResyncedUpdate` passthrough plus bar-engine gap marking (Task 11).
- **Outputs:** non-blocking sends. A full updates channel increments `DroppedUpdates` (the harness/uihub must drain; drops are visible, never silent). Marks are keep-latest by nature — dropping old ones is semantically safe.

- [ ] **Step 1: Write the failing tests**

`engine/internal/md/tape_test.go`:

```go
package md

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestRingWrapsAndSnapshots(t *testing.T) {
	r := newRing(4)
	for i := int64(1); i <= 6; i++ {
		r.append(feed.Tick{Seq: i})
	}
	snap := r.snapshot()
	if len(snap) != 4 || snap[0].Seq != 3 || snap[3].Seq != 6 {
		t.Fatalf("snapshot = %+v, want seqs 3..6", snap)
	}
}
```

`engine/internal/md/core_test.go`:

```go
package md

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// runCore starts a core and returns it plus a drain helper.
func runCore(t *testing.T) (*Core, func() []Update) {
	t.Helper()
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	var got []Update
	drain := func() []Update {
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-time.After(100 * time.Millisecond):
				return got
			}
		}
	}
	return c, drain
}

// ET 2026-07-06 (Monday) 09:30:00 = 2026-07-06T13:30:00Z = epoch 1783344600.
// This MUST be an exact 09:30 ET anchor instant — the cascade tests depend
// on it landing on 10s/1m/5m bucket boundaries.
const t0Ms = int64(1783344600_000)

func tick(seq int64, offMs int64, price float64, vol int64, dir feed.Direction) feed.Tick {
	return feed.Tick{Symbol: "US.AAPL", Seq: seq, TsMs: t0Ms + offMs, Price: price, Volume: vol, Dir: dir}
}

func TestTapeDedupsBySeqWithinDay(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy), tick(2, 500, 100.1, 5, feed.Sell),
	}})
	// Live push overlaps the seed (seq 2) then continues (seq 3).
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(2, 500, 100.1, 5, feed.Sell), tick(3, 900, 100.2, 7, feed.Buy),
	}})
	var tapes []TapeUpdate
	var marks int
	for _, u := range drain() {
		if tu, ok := u.(TapeUpdate); ok {
			tapes = append(tapes, tu)
		}
	}
	for {
		select {
		case <-c.Marks():
			marks++
			continue
		default:
		}
		break
	}
	if len(tapes) != 2 {
		t.Fatalf("TapeUpdates = %d, want 2 (one per accepted batch)", len(tapes))
	}
	if n := len(tapes[0].Ticks) + len(tapes[1].Ticks); n != 3 {
		t.Fatalf("accepted ticks = %d, want 3 (dup seq=2 dropped)", n)
	}
	if marks != 2 {
		t.Fatalf("marks = %d, want 2 (one per batch)", marks)
	}
}

func TestBookAndQuoteReplaceAndEmit(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", Bids: []feed.BookLevel{{Price: 100, Volume: 5}}}})
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 100.5}})
	c.Feed(feed.ConnDownEvent{})
	c.Feed(feed.ResyncedEvent{})
	var kinds []string
	for _, u := range drain() {
		switch u.(type) {
		case BookUpdate:
			kinds = append(kinds, "book")
		case QuoteUpdate:
			kinds = append(kinds, "quote")
		case ConnUpdate:
			kinds = append(kinds, "conn")
		case ResyncedUpdate:
			kinds = append(kinds, "resynced")
		}
	}
	want := []string{"book", "quote", "conn", "resynced"}
	if len(kinds) != 4 {
		t.Fatalf("updates = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("updates order = %v, want %v", kinds, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/md/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`engine/internal/md/update.go` — exactly the types from the Interfaces block above (with `isUpdate()` markers on every update type).

`engine/internal/md/tape.go`:

```go
package md

import "github.com/earlisreal/eTape/engine/internal/feed"

// ring is a fixed-capacity tick ring. Appends overwrite the oldest entry;
// snapshot returns chronological order.
type ring struct {
	buf  []feed.Tick
	head int // next write position
	n    int // valid entries
}

func newRing(capacity int) *ring { return &ring{buf: make([]feed.Tick, capacity)} }

func (r *ring) append(t feed.Tick) {
	r.buf[r.head] = t
	r.head = (r.head + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
}

func (r *ring) snapshot() []feed.Tick {
	out := make([]feed.Tick, 0, r.n)
	start := (r.head - r.n + len(r.buf)) % len(r.buf)
	for i := 0; i < r.n; i++ {
		out = append(out, r.buf[(start+i)%len(r.buf)])
	}
	return out
}
```

`engine/internal/md/book.go` and `quote.go` are trivial latest-maps:

```go
package md

import "github.com/earlisreal/eTape/engine/internal/feed"

type bookStore struct{ m map[string]feed.Book }

func newBookStore() *bookStore { return &bookStore{m: make(map[string]feed.Book)} }

// set replaces the symbol's book (full 10-level replace — cheaper than
// diffing at this depth) and returns it for emission.
func (s *bookStore) set(b feed.Book) feed.Book {
	s.m[b.Symbol] = b
	return b
}
```

(`quoteStore` identical shape over `feed.Quote`.)

`engine/internal/md/core.go`:

```go
// Package md is the market-data core: one goroutine owns books, tape, quotes,
// bars and indicators, consuming feed events and control messages from a
// single inbox and emitting typed updates + last-trade marks. The apply path
// does no I/O and never reads the wall clock — replaying the same events
// reproduces the same state, always.
package md

import (
	"context"
	"sync/atomic"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Config sizes the core. Zero values get defaults.
type Config struct {
	TapeRing   int
	AnchorSecs int64
}

type inMsg interface{ isInMsg() }

type eventMsg struct{ ev feed.Event }
type ensureIndicatorMsg struct {
	id   string
	spec IndicatorSpec
}
type releaseIndicatorMsg struct{ id string }
type seedDailyMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedHistory1mMsg struct {
	symbol string
	bars   []feed.Bar
}

func (eventMsg) isInMsg()            {}
func (ensureIndicatorMsg) isInMsg()  {}
func (releaseIndicatorMsg) isInMsg() {}
func (seedDailyMsg) isInMsg()        {}
func (seedHistory1mMsg) isInMsg()    {}

// Core is the single-writer market-data state machine.
type Core struct {
	cfg     Config
	inbox   chan inMsg
	updates chan Update
	marks   chan Mark
	dropped atomic.Uint64

	// Domain state — touched ONLY inside Run's goroutine.
	books   *bookStore
	quotes  *quoteStore
	tapes   map[string]*ring
	lastSeq map[string]int64 // per-symbol tick dedup high-water
	lastDay map[string]int64 // ET day of lastSeq (sequences restart daily)
	bars    *barEngine       // Task 11
	inds    *indicatorSet    // Task 12
}

// New builds a Core; Run must be started before Feed is called.
func New(cfg Config) *Core {
	if cfg.TapeRing == 0 {
		cfg.TapeRing = 65536
	}
	if cfg.AnchorSecs == 0 {
		cfg.AnchorSecs = session.AnchorSecsDefault
	}
	return &Core{
		cfg:     cfg,
		inbox:   make(chan inMsg, 1024),
		updates: make(chan Update, 8192),
		marks:   make(chan Mark, 1024),
		books:   newBookStore(),
		quotes:  newQuoteStore(),
		tapes:   make(map[string]*ring),
		lastSeq: make(map[string]int64),
		lastDay: make(map[string]int64),
		bars:    newBarEngine(cfg.AnchorSecs),
		inds:    newIndicatorSet(),
	}
}

func (c *Core) Updates() <-chan Update  { return c.updates }
func (c *Core) Marks() <-chan Mark     { return c.marks }
func (c *Core) DroppedUpdates() uint64 { return c.dropped.Load() }

// Feed enqueues a feed event. Blocking by design: the inbox is deep and the
// apply path is allocation-light, so sustained blocking means the core is
// genuinely overloaded — that must surface upstream, not vanish.
func (c *Core) Feed(ev feed.Event) { c.inbox <- eventMsg{ev: ev} }

func (c *Core) EnsureIndicator(id string, spec IndicatorSpec) {
	c.inbox <- ensureIndicatorMsg{id: id, spec: spec}
}
func (c *Core) ReleaseIndicator(id string) { c.inbox <- releaseIndicatorMsg{id: id} }
func (c *Core) SeedDaily(symbol string, bars []feed.Bar) {
	c.inbox <- seedDailyMsg{symbol: symbol, bars: bars}
}
func (c *Core) SeedHistory1m(symbol string, bars []feed.Bar) {
	c.inbox <- seedHistory1mMsg{symbol: symbol, bars: bars}
}

// Run is the single writer. It returns when ctx is done.
func (c *Core) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-c.inbox:
			c.apply(m)
		}
	}
}

func (c *Core) emit(u Update) {
	select {
	case c.updates <- u:
	default:
		c.dropped.Add(1)
	}
}

func (c *Core) mark(m Mark) {
	select {
	case c.marks <- m:
	default: // marks are keep-latest downstream; dropping stale ones is safe
	}
}

func (c *Core) apply(m inMsg) {
	switch msg := m.(type) {
	case eventMsg:
		c.applyEvent(msg.ev)
	case ensureIndicatorMsg:
		c.inds.ensure(c, msg.id, msg.spec) // Task 12
	case releaseIndicatorMsg:
		c.inds.release(msg.id)
	case seedDailyMsg:
		c.bars.seedDaily(c, msg.symbol, msg.bars) // Task 11
	case seedHistory1mMsg:
		c.bars.seedHistory1m(c, msg.symbol, msg.bars)
	}
}

func (c *Core) applyEvent(ev feed.Event) {
	switch e := ev.(type) {
	case feed.TicksEvent:
		c.applyTicks(e)
	case feed.QuoteEvent:
		c.emit(QuoteUpdate{Quote: c.quotes.set(e.Quote)})
	case feed.BookEvent:
		c.emit(BookUpdate{Book: c.books.set(e.Book)})
	case feed.Bars1mEvent:
		c.bars.apply1m(c, e.Bars) // Task 11
	case feed.ConnUpEvent:
		c.emit(ConnUpdate{Up: true})
	case feed.ConnDownEvent:
		c.emit(ConnUpdate{Up: false})
	case feed.ResyncedEvent:
		c.bars.markGaps() // Task 11: next tick-derived bars carry Gap
		c.emit(ResyncedUpdate{})
	}
}

// applyTicks dedups by (day, seq), appends to the tape, drives tick-derived
// bars, and emits one TapeUpdate + one Mark per accepted batch.
func (c *Core) applyTicks(e feed.TicksEvent) {
	if len(e.Ticks) == 0 {
		return
	}
	symbol := e.Ticks[0].Symbol
	accepted := make([]feed.Tick, 0, len(e.Ticks))
	for _, t := range e.Ticks {
		day := session.DayMs(t.TsMs)
		if day != c.lastDay[t.Symbol] {
			c.lastDay[t.Symbol] = day
			c.lastSeq[t.Symbol] = 0
		}
		if t.Seq != 0 && t.Seq <= c.lastSeq[t.Symbol] {
			continue // seed/live overlap or duplicate push
		}
		c.lastSeq[t.Symbol] = t.Seq
		accepted = append(accepted, t)
	}
	if len(accepted) == 0 {
		return
	}
	tape := c.tapes[symbol]
	if tape == nil {
		tape = newRing(c.cfg.TapeRing)
		c.tapes[symbol] = tape
	}
	for _, t := range accepted {
		tape.append(t)
	}
	c.bars.applyTicks(c, accepted) // Task 11 (10s + shadow 1m)
	c.emit(TapeUpdate{Symbol: symbol, Ticks: accepted})
	last := accepted[len(accepted)-1]
	c.mark(Mark{Symbol: last.Symbol, Price: last.Price, TsMs: last.TsMs})
}
```

Until Tasks 11–12 land, stub the two collaborators in their eventual files so this task compiles and ships green on its own. The stubs are **replaced** — not extended — by Tasks 11 and 12:

```go
// bars.go (Task 11 replaces this file)
package md

import "github.com/earlisreal/eTape/engine/internal/feed"

type barEngine struct{ anchorSecs int64 }

func newBarEngine(anchorSecs int64) *barEngine            { return &barEngine{anchorSecs: anchorSecs} }
func (e *barEngine) applyTicks(c *Core, ts []feed.Tick)   {}
func (e *barEngine) apply1m(c *Core, bs []feed.Bar)       {}
func (e *barEngine) seedHistory1m(c *Core, s string, bs []feed.Bar) {}
func (e *barEngine) seedDaily(c *Core, s string, bs []feed.Bar)     {}
func (e *barEngine) markGaps()                            {}
```

```go
// indicator.go (Task 12 replaces this file; the exported types are already
// final — core.go's API references them)
package md

import "github.com/earlisreal/eTape/engine/internal/session"

type IndicatorType string

type IndicatorSpec struct {
	Symbol string
	TF     session.Timeframe
	Type   IndicatorType
	Params map[string]float64
}

type indicatorSet struct{}

func newIndicatorSet() *indicatorSet                                  { return &indicatorSet{} }
func (s *indicatorSet) ensure(c *Core, id string, spec IndicatorSpec) {}
func (s *indicatorSet) release(id string)                             {}
func (s *indicatorSet) onBar(c *Core, b Bar)                          {}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/md/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/md/
git commit -m "feat(engine/md): single-writer core - inbox, tape ring with seq dedup, books, quotes, outputs"
```

---

## Task 10: Tick→bar aggregator (10s and shadow-1m)

Go port of the live-verified prototype `prototypes/tick_to_10s_bars.py`: bucket by exchange timestamp, watermark finalization (a tick for a later bucket finalizes all earlier open bars), buy/sell delta, burst-tolerant so cache seeding replays the same path as live pushes. Parameterized by timeframe so the same engine produces the shadow 1m used for K_1M validation.

**Files:**
- Create: `engine/internal/md/tickagg.go`
- Test: `engine/internal/md/tickagg_test.go`

**Interfaces:**
- Consumes: `feed.Tick`, `session.BucketStartMs`, `md.Bar`.
- Produces (used by Task 11):

```go
func newTickAgg(symbol string, tf session.Timeframe) *tickAgg
// addTick returns the emissions caused by t, in order: zero or more FINAL
// bars (watermark-closed buckets), then the in-progress bar for t's bucket.
// gapFlag marks the first NEW bucket opened after a resync.
func (a *tickAgg) addTick(t feed.Tick, gapFlag bool) []Bar
func (a *tickAgg) lateDrops() uint64 // ticks older than the watermark, dropped
```

**Semantics (from the prototype, made exact):**
1. `bucket = session.BucketStartMs(t.TsMs, tf)`.
2. If `bucket` is older than the newest finalized bucket → late tick: drop, count, no emission (honesty: never rewrite a finalized bar).
3. If `bucket` opens a new bucket: every open bucket `< bucket` finalizes (chronological order), each emitted with `InProgress: false`. (Multiple buckets can be open only transiently during seed bursts — the map handles it.)
4. Add the tick: first tick sets O; H/L extend; C follows; V accumulates; `Dir == Buy` → BuyV, `Sell` → SellV, Neutral → neither; Ticks increments.
5. Emit the updated bucket with `InProgress: true` (and `Gap: gapFlag` if this tick opened a new bucket right after a resync).
6. Quiet symbols hold partials past wall-clock end — finalization happens **only** on next-bucket evidence, never on a timer (matches the UI spec's in-progress semantics and keeps the core clock-free).

- [ ] **Step 1: Write the failing tests**

`engine/internal/md/tickagg_test.go`:

```go
package md

import (
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Reuses t0Ms (09:30:00 ET) and tick() from core_test.go.

func TestTickAggWatermarkAndDelta(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)

	// Two ticks in bucket [09:30:00, 09:30:10).
	got := a.addTick(tick(1, 1_000, 100.0, 10, feed.Buy), false)
	if len(got) != 1 || !got[0].InProgress || got[0].O != 100.0 {
		t.Fatalf("first emission = %+v", got)
	}
	got = a.addTick(tick(2, 9_000, 99.5, 5, feed.Sell), false)
	b := got[len(got)-1]
	if b.H != 100.0 || b.L != 99.5 || b.C != 99.5 || b.V != 15 || b.BuyV != 10 || b.SellV != 5 || b.Ticks != 2 {
		t.Fatalf("in-progress bar = %+v", b)
	}

	// A tick in the NEXT bucket finalizes the first (watermark).
	got = a.addTick(tick(3, 12_000, 99.8, 3, feed.Neutral), false)
	if len(got) != 2 {
		t.Fatalf("emissions = %+v, want [final, in-progress]", got)
	}
	if got[0].InProgress || got[0].BucketMs != t0Ms || got[0].V != 15 {
		t.Fatalf("final bar = %+v", got[0])
	}
	if !got[1].InProgress || got[1].BucketMs != t0Ms+10_000 || got[1].BuyV != 0 || got[1].SellV != 0 {
		t.Fatalf("new in-progress bar = %+v (neutral tick adds no delta)", got[1])
	}
}

func TestTickAggSkipsEmptyBucketsAndDropsLate(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	a.addTick(tick(1, 0, 100, 1, feed.Buy), false)
	// Jump 35s: bucket 09:30:30. Only the open bucket finalizes — empty
	// buckets in between are never fabricated.
	got := a.addTick(tick(2, 35_000, 101, 1, feed.Buy), false)
	if len(got) != 2 || got[0].BucketMs != t0Ms || got[1].BucketMs != t0Ms+30_000 {
		t.Fatalf("emissions = %+v", got)
	}
	// A tick for the already-finalized first bucket is dropped.
	if got := a.addTick(tick(3, 5_000, 100.5, 1, feed.Buy), false); got != nil {
		t.Fatalf("late tick emitted %+v, want nothing", got)
	}
	if a.lateDrops() != 1 {
		t.Fatalf("lateDrops = %d, want 1", a.lateDrops())
	}
}

// Seed/live equivalence: the same tick stream produces identical bars whether
// it arrives as one seed burst or split across seed + live batches.
func TestTickAggSeedLiveEquivalence(t *testing.T) {
	ticks := []feed.Tick{
		tick(1, 500, 100, 10, feed.Buy), tick(2, 4_000, 100.2, 5, feed.Sell),
		tick(3, 11_000, 100.1, 8, feed.Buy), tick(4, 19_000, 100.4, 2, feed.Neutral),
		tick(5, 21_000, 100.3, 6, feed.Sell),
	}
	collect := func(splits ...[]feed.Tick) []Bar {
		a := newTickAgg("US.AAPL", session.TF10s)
		var finals []Bar
		for _, batch := range splits {
			for _, tk := range batch {
				for _, b := range a.addTick(tk, false) {
					if !b.InProgress {
						finals = append(finals, b)
					}
				}
			}
		}
		return finals
	}
	oneBurst := collect(ticks)
	split := collect(ticks[:2], ticks[2:])
	if !reflect.DeepEqual(oneBurst, split) {
		t.Fatalf("burst vs split finals differ:\n%+v\n%+v", oneBurst, split)
	}
}

func TestTickAggShadow1m(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF1m)
	a.addTick(tick(1, 1_000, 100, 10, feed.Buy), false)
	got := a.addTick(tick(2, 61_000, 101, 5, feed.Buy), false)
	if len(got) != 2 || got[0].BucketMs != t0Ms || got[0].TF != session.TF1m || got[0].InProgress {
		t.Fatalf("1m watermark emissions = %+v", got)
	}
}

func TestTickAggGapFlag(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	a.addTick(tick(1, 0, 100, 1, feed.Buy), false)
	got := a.addTick(tick(2, 12_000, 100, 1, feed.Buy), true) // first bucket after resync
	nb := got[len(got)-1]
	if !nb.Gap {
		t.Fatalf("post-resync bar not gap-flagged: %+v", nb)
	}
	// The flagged bar KEEPS its flag on later updates within the bucket...
	got = a.addTick(tick(3, 13_000, 100, 1, feed.Buy), false)
	if !got[len(got)-1].Gap {
		t.Fatal("gap flag lost on an update to the flagged bucket")
	}
	// ...and once the caller clears its pending state (gapFlag=false), the
	// next bucket is clean.
	got = a.addTick(tick(4, 22_000, 100, 1, feed.Buy), false)
	if got[len(got)-1].Gap {
		t.Fatal("gap flag leaked onto the following bucket")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/md/ -run TestTickAgg -v`
Expected: FAIL — `newTickAgg` undefined.

- [ ] **Step 3: Implement**

`engine/internal/md/tickagg.go`:

```go
package md

import (
	"sort"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// tickAgg builds bars of one timeframe from a tick stream — the Go port of
// prototypes/tick_to_10s_bars.py (verified live 2026-07-03): bucket by
// EXCHANGE timestamp, finalize on next-bucket evidence (watermark), track
// buy/sell volume delta. Burst-tolerant: cache seeding replays the exact
// same path as live pushes.
type tickAgg struct {
	symbol string
	tf     session.Timeframe

	open           map[int64]*Bar // in-progress buckets
	finalizedAfter int64          // newest finalized bucket; older ticks are late
	late           uint64
}

func newTickAgg(symbol string, tf session.Timeframe) *tickAgg {
	return &tickAgg{symbol: symbol, tf: tf, open: make(map[int64]*Bar), finalizedAfter: -1}
}

func (a *tickAgg) lateDrops() uint64 { return a.late }

func (a *tickAgg) addTick(t feed.Tick, gapFlag bool) []Bar {
	bucket := session.BucketStartMs(t.TsMs, a.tf)
	if a.finalizedAfter >= 0 && bucket <= a.finalizedAfter {
		a.late++
		return nil
	}

	var out []Bar
	b, exists := a.open[bucket]
	if !exists {
		// Watermark: a tick for a new bucket closes all earlier open buckets,
		// oldest first. Empty buckets in between are never fabricated.
		var older []int64
		for k := range a.open {
			if k < bucket {
				older = append(older, k)
			}
		}
		sort.Slice(older, func(i, j int) bool { return older[i] < older[j] })
		for _, k := range older {
			fin := *a.open[k]
			fin.InProgress = false
			delete(a.open, k)
			if k > a.finalizedAfter {
				a.finalizedAfter = k
			}
			out = append(out, fin)
		}
		b = &Bar{
			Symbol: a.symbol, TF: a.tf, BucketMs: bucket,
			O: t.Price, H: t.Price, L: t.Price,
			InProgress: true, Gap: gapFlag,
		}
		a.open[bucket] = b
	}

	if t.Price > b.H {
		b.H = t.Price
	}
	if t.Price < b.L {
		b.L = t.Price
	}
	b.C = t.Price
	b.V += t.Volume
	b.Ticks++
	switch t.Dir {
	case feed.Buy:
		b.BuyV += t.Volume
	case feed.Sell:
		b.SellV += t.Volume
	}
	out = append(out, *b)
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/md/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/md/tickagg.go internal/md/tickagg_test.go
git commit -m "feat(engine/md): tick-to-bar aggregator (10s/shadow-1m) - watermark, delta, seed/live-equivalent"
```

## Task 11: Bar engine — authoritative 1m, higher timeframes, daily/W/M, validation, gaps

The bar architecture in one component: 10s from ticks (Task 10's aggregator), **1m authoritative from K_1M** with a tick-derived shadow for validation and buy/sell delta, 5m–60m recomputed from 1m (session-anchored), daily derived live + replaced by official history, W/M derived from daily, gap flagging after resyncs.

**Files:**
- Create: `engine/internal/md/bars.go` (replaces the Task 9 stub)
- Modify: `engine/internal/md/tickagg.go` (add one accessor)
- Modify: `engine/internal/md/core.go` (add the `barOut` helper)
- Test: `engine/internal/md/bars_test.go`

**Interfaces:**
- Consumes: `tickAgg` (Task 10), `session` bucketing, `Core.emit`.
- Produces (used by Tasks 12–13):

```go
func newBarEngine(anchorSecs int64) *barEngine
func (e *barEngine) applyTicks(c *Core, ticks []feed.Tick)        // same-symbol batch
func (e *barEngine) apply1m(c *Core, bars []feed.Bar)             // K_1M push or cache seed
func (e *barEngine) seedHistory1m(c *Core, symbol string, bars []feed.Bar)
func (e *barEngine) seedDaily(c *Core, symbol string, bars []feed.Bar)
func (e *barEngine) markGaps()                                    // resync: flag next 10s buckets
func (e *barEngine) finalizedBars(symbol string, tf session.Timeframe) []Bar // indicator seeding
// tickagg.go addition:
func (a *tickAgg) openBar(bucketMs int64) *Bar
// core.go addition — every bar emission goes through one door:
func (c *Core) barOut(b Bar) { c.emit(BarUpdate{Bar: b}); c.inds.onBar(c, b) }
```

**Semantics (encode exactly):**
1. **Authoritative 1m** (`apply1m`): watermark upsert. Incoming bucket > series last → previous last (if in-progress) finalizes and emits, then trigger validation for it; new bar appends in-progress. Same bucket → values update in place. Older bucket (cache-seed overlap, deep history) → insert/update as finalized, emit only when values changed. Every accepted 1m change merges delta fields from the shadow (final map first, then the shadow's open bucket), then cascades (rule 3) and re-derives daily (rule 4).
2. **Shadow 1m + 10s** (`applyTicks`): route each tick through both aggregators. 10s emissions are stored in the 10s series and emitted via `barOut` (with `Gap` flags per Task 10). Shadow emissions are **never** emitted as bars — shadow finals go to `shadowFinals[bucket]`, trigger validation, and back-merge delta into the matching authoritative 1m bar (emit + cascade when the delta changed); a forming shadow back-merges into a matching *in-progress* 1m bar the same way. On an ET day change (first tick of a new day), `shadowFinals`/`compared` reset — moomoo sequences and sessions are daily.
3. **Cascade (5m/15m/30m/60m):** on any 1m change at bucket `m`, for each higher tf recompute the containing bar from the 1m bars in `[hb, hb+span)` — O from first, H/L extremes, C from last, sums for V/BuyV/SellV/Ticks. `InProgress = !(any 1m bar exists with bucket ≥ hb+span)` — next-bucket evidence, uniform with every other finalization rule (no timers, ever). Emit only on change.
4. **Daily:** derived live from the day's 1m bars, always `InProgress: true` (the official auction-priced bar is fetched, never aggregated — spec). `seedDaily` upserts official bars as finalized and marks their buckets protected (`dailyOfficial`), so live derivation skips them. Each daily change re-derives W/M.
5. **W/M:** recomputed from daily bars whose W/M bucket matches. `InProgress = any constituent in-progress OR the newest daily bar is inside this period`.
6. **Validation:** when both the authoritative and shadow 1m for a bucket are final, compare once (`compared` guard): O/H/L/C beyond `1e-6` or volume beyond `max(100, 2%)` emits `MismatchUpdate` (alarm, not blocker — K_1M wins for display; thresholds are constants, tuned after Monday's live session).
7. **Gaps:** `markGaps` sets a per-symbol pending flag; the next *newly opened* 10s bucket per symbol carries `Gap: true` (Task 10's mechanism), then the flag clears. 1m+ self-heal via cache re-seed and are not flagged.

- [ ] **Step 1: Write the failing tests**

`engine/internal/md/bars_test.go` (reuses `t0Ms`, `tick()`, `runCore` from earlier test files):

```go
package md

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func bar1m(offMin int, o, h, l, cl float64, v int64) feed.Bar {
	return feed.Bar{Symbol: "US.AAPL", BucketMs: t0Ms + int64(offMin)*60_000, O: o, H: h, L: l, C: cl, Volume: v}
}

// collectBars filters BarUpdates for one timeframe out of drained updates.
func collectBars(us []Update, tf session.Timeframe) []Bar {
	var out []Bar
	for _, u := range us {
		if bu, ok := u.(BarUpdate); ok && bu.Bar.TF == tf {
			out = append(out, bu.Bar)
		}
	}
	return out
}

func TestAuth1mWatermarkFinalizes(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101.5, 99, 101, 1500)}}) // same bucket refresh
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 101, 102, 100.8, 101.9, 900)}})
	bars := collectBars(drain(), session.TF1m)
	if len(bars) != 4 { // in-progress, refresh, finalized(0), in-progress(1)
		t.Fatalf("1m updates = %d (%+v), want 4", len(bars), bars)
	}
	if bars[2].InProgress || bars[2].H != 101.5 {
		t.Fatalf("finalized bar = %+v, want final with refreshed H", bars[2])
	}
	if !bars[3].InProgress || bars[3].BucketMs != t0Ms+60_000 {
		t.Fatalf("new forming bar = %+v", bars[3])
	}
}

func TestSeedOverlapIsIdempotent(t *testing.T) {
	c, drain := runCore(t)
	seed := []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000), bar1m(1, 100.5, 102, 100, 101.5, 800)}
	c.Feed(feed.Bars1mEvent{Bars: seed, Seed: true})
	first := len(collectBars(drain(), session.TF1m))
	// The same bars again (reconnect re-seed): no value changed → no emission
	// for bar 0; bar 1 is the forming last, refresh emits are acceptable but
	// values must not change.
	c.Feed(feed.Bars1mEvent{Bars: seed, Seed: true})
	again := collectBars(drain()[first:], session.TF1m)
	for _, b := range again {
		if b.BucketMs == t0Ms && b.H != 101 {
			t.Fatalf("re-seed mutated a finalized bar: %+v", b)
		}
	}
}

func TestCascadeAnchoredAggregation(t *testing.T) {
	c, drain := runCore(t)
	// Five 1m bars 09:30..09:34 → one 5m bucket [09:30, 09:35); the 09:35 bar
	// finalizes it.
	var bars []feed.Bar
	for i := 0; i < 5; i++ {
		bars = append(bars, bar1m(i, 100+float64(i), 100.5+float64(i), 99.5+float64(i), 100.2+float64(i), 100))
	}
	c.Feed(feed.Bars1mEvent{Bars: bars})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(5, 105, 106, 104, 105.5, 50)}})
	fives := collectBars(drain(), session.TF5m)
	if len(fives) == 0 {
		t.Fatal("no 5m bars emitted")
	}
	last := fives[len(fives)-1]
	final5 := fives[len(fives)-2]
	if final5.InProgress || final5.BucketMs != t0Ms || final5.O != 100 || final5.C != 104.2 || final5.V != 500 {
		t.Fatalf("finalized 5m = %+v", final5)
	}
	if !last.InProgress || last.BucketMs != t0Ms+5*60_000 {
		t.Fatalf("forming 5m = %+v", last)
	}
}

func TestShadowDeltaMergesIntoAuth1m(t *testing.T) {
	c, drain := runCore(t)
	// Ticks build the shadow 1m for [09:30,09:31): buy 30, sell 10.
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(1, 1_000, 100, 30, feed.Buy), tick(2, 30_000, 100.1, 10, feed.Sell),
	}})
	// Authoritative K_1M bar for the same bucket arrives.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 100.2, 99.9, 100.1, 45)}})
	bars := collectBars(drain(), session.TF1m)
	got := bars[len(bars)-1]
	if got.BuyV != 30 || got.SellV != 10 {
		t.Fatalf("auth 1m delta = buy %d sell %d, want 30/10", got.BuyV, got.SellV)
	}
}

func TestMismatchEmitsOnDivergence(t *testing.T) {
	c, drain := runCore(t)
	// Shadow finalizes bucket 0 via a tick in bucket 1.
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(1, 1_000, 100, 10, feed.Buy), tick(2, 61_000, 100.5, 5, feed.Buy),
	}})
	// Authoritative bar for bucket 0 disagrees on close and volume, then
	// finalizes via bucket 1's bar.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 100.9, 100, 100.9, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100.9, 101, 100.5, 100.6, 200)}})
	var mismatches []MismatchUpdate
	for _, u := range drain() {
		if mu, ok := u.(MismatchUpdate); ok {
			mismatches = append(mismatches, mu)
		}
	}
	if len(mismatches) != 1 || mismatches[0].BucketMs != t0Ms {
		t.Fatalf("mismatches = %+v, want exactly one for bucket 0", mismatches)
	}
}

func TestDerivedDailyAndOfficialReplacement(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)}})
	dailies := collectBars(drain(), session.TFDay)
	if len(dailies) == 0 || !dailies[len(dailies)-1].InProgress {
		t.Fatalf("derived daily = %+v, want in-progress", dailies)
	}
	day := session.BucketStartMs(t0Ms, session.TFDay)
	c.SeedDaily("US.AAPL", []feed.Bar{{Symbol: "US.AAPL", BucketMs: day, O: 99.8, H: 101.2, L: 98.9, C: 100.7, Volume: 5_000_000}})
	dailies = collectBars(drain(), session.TFDay)
	official := dailies[len(dailies)-1]
	if official.InProgress || official.O != 99.8 || official.V != 5_000_000 {
		t.Fatalf("official daily = %+v", official)
	}
	// Further 1m updates must NOT overwrite the official bar.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100.5, 100.6, 100.4, 100.5, 10)}})
	for _, b := range collectBars(drain(), session.TFDay) {
		if b.BucketMs == day && b.O != 99.8 {
			t.Fatalf("official daily overwritten by derivation: %+v", b)
		}
	}
}

func TestWeeklyDerivedFromDaily(t *testing.T) {
	c, drain := runCore(t)
	// Mon + Tue official dailies of week 2026-07-06.
	mon := session.BucketStartMs(t0Ms, session.TFDay)
	c.SeedDaily("US.AAPL", []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: mon, O: 100, H: 105, L: 99, C: 104, Volume: 1000},
		{Symbol: "US.AAPL", BucketMs: mon + 86_400_000, O: 104, H: 107, L: 103, C: 106, Volume: 1200},
	})
	weeks := collectBars(drain(), session.TFWeek)
	w := weeks[len(weeks)-1]
	if w.O != 100 || w.H != 107 || w.C != 106 || w.V != 2200 {
		t.Fatalf("weekly = %+v", w)
	}
	if !w.InProgress {
		t.Fatal("current week must be in-progress (newest daily is inside it)")
	}
}

func TestGapFlagAfterResync(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(1, 1_000, 100, 1, feed.Buy)}})
	c.Feed(feed.ResyncedEvent{})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(2, 25_000, 100.5, 1, feed.Buy)}}) // new 10s bucket
	tens := collectBars(drain(), session.TF10s)
	last := tens[len(tens)-1]
	if !last.Gap {
		t.Fatalf("first 10s bar after resync not gap-flagged: %+v", last)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/md/ -run 'TestAuth1m|TestSeedOverlap|TestCascade|TestShadowDelta|TestMismatch|TestDerivedDaily|TestWeekly|TestGapFlag' -v`
Expected: FAIL — the Task 9 stub does nothing (assertions on emissions fail).

- [ ] **Step 3: Implement**

Add to `engine/internal/md/tickagg.go`:

```go
// openBar returns the in-progress bar for bucketMs, or nil.
func (a *tickAgg) openBar(bucketMs int64) *Bar { return a.open[bucketMs] }
```

Add to `engine/internal/md/core.go` (single emission door — Task 12's indicator routing hooks in here):

```go
// barOut is the single door for bar emissions: update stream + indicators.
func (c *Core) barOut(b Bar) {
	c.emit(BarUpdate{Bar: b})
	c.inds.onBar(c, b)
}
```

`engine/internal/md/bars.go`:

```go
package md

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Validation thresholds for K_1M vs tick-derived 1m (alarm, not blocker —
// K_1M wins for display). Tuned after Monday's live session.
const (
	mismatchPriceTol = 1e-6
	mismatchVolPct   = 0.02
	mismatchVolAbs   = 100
)

var cascadeTFs = []session.Timeframe{session.TF5m, session.TF15m, session.TF30m, session.TF60m}

// series is one symbol+timeframe bar sequence, ascending by BucketMs.
type series struct {
	bars []Bar
}

func (s *series) idx(bucketMs int64) int {
	return sort.Search(len(s.bars), func(i int) bool { return s.bars[i].BucketMs >= bucketMs })
}

func (s *series) get(bucketMs int64) *Bar {
	i := s.idx(bucketMs)
	if i < len(s.bars) && s.bars[i].BucketMs == bucketMs {
		return &s.bars[i]
	}
	return nil
}

// upsert inserts b in order or replaces the existing bar; reports change.
func (s *series) upsert(b Bar) bool {
	i := s.idx(b.BucketMs)
	if i < len(s.bars) && s.bars[i].BucketMs == b.BucketMs {
		if s.bars[i] == b {
			return false
		}
		s.bars[i] = b
		return true
	}
	s.bars = append(s.bars, Bar{})
	copy(s.bars[i+1:], s.bars[i:])
	s.bars[i] = b
	return true
}

func (s *series) last() *Bar {
	if len(s.bars) == 0 {
		return nil
	}
	return &s.bars[len(s.bars)-1]
}

func (s *series) maxBucket() int64 {
	if b := s.last(); b != nil {
		return b.BucketMs
	}
	return -1
}

// rangeBars returns bars with BucketMs in [from, to).
func (s *series) rangeBars(from, to int64) []Bar {
	lo := s.idx(from)
	hi := s.idx(to)
	return s.bars[lo:hi]
}

func (s *series) finalized() []Bar {
	out := make([]Bar, 0, len(s.bars))
	for _, b := range s.bars {
		if !b.InProgress {
			out = append(out, b)
		}
	}
	return out
}

// symbolBars is all bar state for one symbol.
type symbolBars struct {
	symbol string
	agg10  *tickAgg
	shadow *tickAgg

	series map[session.Timeframe]*series

	shadowFinals  map[int64]Bar  // tick-derived 1m finals: delta source + validation
	compared      map[int64]bool // per-bucket validation guard
	dailyOfficial map[int64]bool // K_DAY-seeded buckets: derivation must not touch
	gapPending    bool
	curDay        int64
}

// barEngine owns every derived bar series. It is called only from the Core's
// single writer goroutine — no locks, no clock, event timestamps only.
type barEngine struct {
	anchorSecs int64
	symbols    map[string]*symbolBars
}

func newBarEngine(anchorSecs int64) *barEngine {
	return &barEngine{anchorSecs: anchorSecs, symbols: make(map[string]*symbolBars)}
}

func (e *barEngine) sym(symbol string) *symbolBars {
	sb := e.symbols[symbol]
	if sb == nil {
		sb = &symbolBars{
			symbol:        symbol,
			agg10:         newTickAgg(symbol, session.TF10s),
			shadow:        newTickAgg(symbol, session.TF1m),
			series:        make(map[session.Timeframe]*series),
			shadowFinals:  make(map[int64]Bar),
			compared:      make(map[int64]bool),
			dailyOfficial: make(map[int64]bool),
		}
		for _, tf := range []session.Timeframe{session.TF10s, session.TF1m, session.TF5m,
			session.TF15m, session.TF30m, session.TF60m, session.TFDay, session.TFWeek, session.TFMonth} {
			sb.series[tf] = &series{}
		}
		e.symbols[symbol] = sb
	}
	return sb
}

func (e *barEngine) markGaps() {
	for _, sb := range e.symbols {
		sb.gapPending = true
	}
}

func (e *barEngine) finalizedBars(symbol string, tf session.Timeframe) []Bar {
	sb := e.symbols[symbol]
	if sb == nil {
		return nil
	}
	return sb.series[tf].finalized()
}

// applyTicks drives the 10s series (displayed) and the shadow 1m (internal:
// validation + delta). Ticks arrive deduped from the core.
func (e *barEngine) applyTicks(c *Core, ticks []feed.Tick) {
	if len(ticks) == 0 {
		return
	}
	sb := e.sym(ticks[0].Symbol)
	for _, t := range ticks {
		if day := session.DayMs(t.TsMs); day != sb.curDay {
			sb.curDay = day
			sb.shadowFinals = make(map[int64]Bar)
			sb.compared = make(map[int64]bool)
		}
		for _, b := range sb.agg10.addTick(t, sb.gapPending) {
			if b.Gap && b.InProgress {
				sb.gapPending = false
			}
			sb.series[session.TF10s].upsert(b)
			c.barOut(b)
		}
		for _, b := range sb.shadow.addTick(t, false) {
			if b.InProgress {
				e.mergeShadowDelta(c, sb, b, false)
			} else {
				sb.shadowFinals[b.BucketMs] = b
				e.mergeShadowDelta(c, sb, b, true)
				e.validate(c, sb, b.BucketMs)
			}
		}
	}
}

// mergeShadowDelta copies BuyV/SellV/Ticks from a shadow bar into the
// matching authoritative 1m bar. final=false only touches an in-progress
// auth bar (a finalized bar's delta is settled by the shadow final).
func (e *barEngine) mergeShadowDelta(c *Core, sb *symbolBars, shadow Bar, final bool) {
	ab := sb.series[session.TF1m].get(shadow.BucketMs)
	if ab == nil || (!final && !ab.InProgress) {
		return
	}
	if ab.BuyV == shadow.BuyV && ab.SellV == shadow.SellV && ab.Ticks == shadow.Ticks {
		return
	}
	ab.BuyV, ab.SellV, ab.Ticks = shadow.BuyV, shadow.SellV, shadow.Ticks
	c.barOut(*ab)
	e.cascade(c, sb, ab.BucketMs)
}

// apply1m upserts authoritative K_1M bars (push or cache seed) with the
// watermark rule and drives everything derived from 1m.
func (e *barEngine) apply1m(c *Core, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(bars[0].Symbol)
	oneM := sb.series[session.TF1m]
	for _, raw := range bars {
		nb := Bar{
			Symbol: raw.Symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
			InProgress: true,
		}
		e.fillDelta(sb, &nb)
		last := oneM.last()
		finalizedPrev := int64(-1)
		switch {
		case last == nil || nb.BucketMs > last.BucketMs:
			if last != nil && last.InProgress {
				last.InProgress = false
				c.barOut(*last)
				finalizedPrev = last.BucketMs
			}
			oneM.upsert(nb)
			c.barOut(nb)
		case nb.BucketMs == last.BucketMs:
			nb.InProgress = last.InProgress
			if oneM.upsert(nb) {
				c.barOut(nb)
			}
		default: // older bucket: seed overlap / history — always finalized
			nb.InProgress = false
			if oneM.upsert(nb) {
				c.barOut(nb)
				e.validate(c, sb, nb.BucketMs)
			}
		}
		if finalizedPrev >= 0 {
			e.validate(c, sb, finalizedPrev)
			// The finalized bar's HIGHER buckets must recompute too — the new
			// bucket may have crossed a 5m/15m/... boundary, and only
			// next-bucket evidence (now present) can flip them to final.
			e.cascade(c, sb, finalizedPrev)
		}
		e.cascade(c, sb, nb.BucketMs)
		e.deriveDaily(c, sb, nb.BucketMs)
	}
}

// fillDelta seeds a fresh auth 1m bar's delta fields from the shadow.
func (e *barEngine) fillDelta(sb *symbolBars, b *Bar) {
	if sf, ok := sb.shadowFinals[b.BucketMs]; ok {
		b.BuyV, b.SellV, b.Ticks = sf.BuyV, sf.SellV, sf.Ticks
		return
	}
	if ob := sb.shadow.openBar(b.BucketMs); ob != nil {
		b.BuyV, b.SellV, b.Ticks = ob.BuyV, ob.SellV, ob.Ticks
	}
}

// seedHistory1m inserts deep-history 1m bars (all finalized) without
// disturbing the live forming bar, then re-derives everything once per bar.
func (e *barEngine) seedHistory1m(c *Core, symbol string, bars []feed.Bar) {
	sb := e.sym(symbol)
	oneM := sb.series[session.TF1m]
	forming := int64(-1)
	if lb := oneM.last(); lb != nil && lb.InProgress {
		forming = lb.BucketMs
	}
	for _, raw := range bars {
		if raw.BucketMs == forming {
			continue // the live stream owns the forming bar
		}
		nb := Bar{
			Symbol: symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		e.fillDelta(sb, &nb)
		if oneM.upsert(nb) {
			c.barOut(nb)
			e.cascade(c, sb, nb.BucketMs)
			e.deriveDaily(c, sb, nb.BucketMs)
		}
	}
}

// cascade recomputes the higher intraday bars containing the 1m bucket m.
func (e *barEngine) cascade(c *Core, sb *symbolBars, m int64) {
	oneM := sb.series[session.TF1m]
	for _, tf := range cascadeTFs {
		span, _ := session.IntradaySpanSecs(tf)
		spanMs := span * 1000
		hb := session.BucketStartMsAnchored(m, tf, e.anchorSecs)
		members := oneM.rangeBars(hb, hb+spanMs)
		if len(members) == 0 {
			continue
		}
		nb := foldBars(sb.symbol, tf, hb, members)
		nb.InProgress = oneM.maxBucket() < hb+spanMs // next-bucket evidence
		if sb.series[tf].upsert(nb) {
			c.barOut(nb)
		}
	}
}

// deriveDaily maintains the live derived daily bar (always in-progress —
// official K_DAY bars are fetched, never aggregated, and replace it).
func (e *barEngine) deriveDaily(c *Core, sb *symbolBars, m int64) {
	day := session.BucketStartMs(m, session.TFDay)
	if sb.dailyOfficial[day] {
		return
	}
	members := sb.series[session.TF1m].rangeBars(day, day+86_400_000)
	if len(members) == 0 {
		return
	}
	nb := foldBars(sb.symbol, session.TFDay, day, members)
	nb.InProgress = true
	if sb.series[session.TFDay].upsert(nb) {
		c.barOut(nb)
		e.deriveWM(c, sb, day)
	}
}

// seedDaily upserts official (auction-priced, forward-adjusted) daily bars.
func (e *barEngine) seedDaily(c *Core, symbol string, bars []feed.Bar) {
	sb := e.sym(symbol)
	for _, raw := range bars {
		nb := Bar{
			Symbol: symbol, TF: session.TFDay, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		sb.dailyOfficial[raw.BucketMs] = true
		if sb.series[session.TFDay].upsert(nb) {
			c.barOut(nb)
			e.deriveWM(c, sb, raw.BucketMs)
		}
	}
}

// deriveWM recomputes the weekly and monthly bars containing day.
func (e *barEngine) deriveWM(c *Core, sb *symbolBars, day int64) {
	daily := sb.series[session.TFDay]
	newest := daily.maxBucket()
	for _, tf := range []session.Timeframe{session.TFWeek, session.TFMonth} {
		hb := session.BucketStartMs(day, tf)
		var members []Bar
		anyInProgress := false
		for _, d := range daily.bars {
			if session.BucketStartMs(d.BucketMs, tf) == hb {
				members = append(members, d)
				anyInProgress = anyInProgress || d.InProgress
			}
		}
		if len(members) == 0 {
			continue
		}
		nb := foldBars(sb.symbol, tf, hb, members)
		nb.InProgress = anyInProgress || session.BucketStartMs(newest, tf) == hb
		if sb.series[tf].upsert(nb) {
			c.barOut(nb)
		}
	}
}

// foldBars aggregates ordered constituent bars into one bar of tf at bucket.
func foldBars(symbol string, tf session.Timeframe, bucket int64, members []Bar) Bar {
	nb := Bar{
		Symbol: symbol, TF: tf, BucketMs: bucket,
		O: members[0].O, H: members[0].H, L: members[0].L, C: members[len(members)-1].C,
	}
	for _, mb := range members {
		nb.H = math.Max(nb.H, mb.H)
		nb.L = math.Min(nb.L, mb.L)
		nb.V += mb.V
		nb.BuyV += mb.BuyV
		nb.SellV += mb.SellV
		nb.Ticks += mb.Ticks
	}
	return nb
}

// validate compares finalized authoritative vs shadow 1m for one bucket,
// once. Divergence is an alarm (MismatchUpdate), never a blocker.
func (e *barEngine) validate(c *Core, sb *symbolBars, bucketMs int64) {
	if sb.compared[bucketMs] {
		return
	}
	shadow, ok := sb.shadowFinals[bucketMs]
	if !ok {
		return
	}
	auth := sb.series[session.TF1m].get(bucketMs)
	if auth == nil || auth.InProgress {
		return
	}
	sb.compared[bucketMs] = true
	var details []string
	price := func(name string, a, b float64) {
		if math.Abs(a-b) > mismatchPriceTol {
			details = append(details, fmt.Sprintf("%s kline=%g tick=%g", name, a, b))
		}
	}
	price("O", auth.O, shadow.O)
	price("H", auth.H, shadow.H)
	price("L", auth.L, shadow.L)
	price("C", auth.C, shadow.C)
	dv := auth.V - shadow.V
	if dv < 0 {
		dv = -dv
	}
	volTol := int64(float64(auth.V) * mismatchVolPct)
	if volTol < mismatchVolAbs {
		volTol = mismatchVolAbs
	}
	if dv > volTol {
		details = append(details, fmt.Sprintf("V kline=%d tick=%d", auth.V, shadow.V))
	}
	if len(details) > 0 {
		c.emit(MismatchUpdate{Symbol: sb.symbol, BucketMs: bucketMs, Detail: strings.Join(details, "; ")})
	}
}
```

Wire the Task 9 core stubs to the real methods: `applyEvent`'s `Bars1mEvent` case calls `c.bars.apply1m(c, e.Bars)`, `applyTicks` calls `c.bars.applyTicks(c, accepted)`, `ResyncedEvent` calls `c.bars.markGaps()`, and the seed messages call `seedDaily`/`seedHistory1m` (these calls are already written in Task 9's `core.go` — this task replaces the stub bodies).

Note on the `series.upsert` change-detection: `Bar` is comparable (no slices/maps/pointers) — `s.bars[i] == b` is valid Go. Keep it that way; don't add slice fields to `Bar`.

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/md/ -v`
Expected: PASS — all bar-engine tests plus Tasks 9–10 unchanged.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/md/bars.go internal/md/bars_test.go internal/md/tickagg.go internal/md/core.go
git commit -m "feat(engine/md): bar engine - auth 1m, anchored 5m-60m, derived daily/W/M, validation, gap flags"
```

## Task 12: Indicators — VWAP, EMA, SMA, MACD, VOLUME, DELTA

The v1 catalog (UI spec) with the streaming contract: `fold(finalized bar)` advances state O(1); `points(bar)` computes output from last-folded state **without mutating it** — a live EMA never compounds partials. Instances are `(symbol, timeframe, type, params)` keyed by the requester's `instanceId`, refcounted, seeded from history on create. Parameter names and defaults mirror the UI catalog exactly (`period` 9/20; MACD `fast` 12, `slow` 26, `signal` 9); multi-slot output keys are `instanceId#slot` with MACD slots `macd`/`signal`/`hist` (UI Task 7 contract).

**Files:**
- Create: `engine/internal/md/indicator.go` (registry — replaces the Task 9 stub), `engine/internal/md/ind_calcs.go` (the six calculators)
- Test: `engine/internal/md/indicator_test.go`

**Interfaces:**
- Consumes: `Bar`, `barEngine.finalizedBars`, `Core.emit`, `session`.
- Produces:

```go
type IndicatorType string
const (
	IndVWAP   IndicatorType = "VWAP"
	IndEMA    IndicatorType = "EMA"
	IndSMA    IndicatorType = "SMA"
	IndMACD   IndicatorType = "MACD"
	IndVolume IndicatorType = "VOLUME"
	IndDelta  IndicatorType = "DELTA"
)
type IndicatorSpec struct {
	Symbol string
	TF     session.Timeframe
	Type   IndicatorType
	Params map[string]float64 // catalog keys; missing keys take defaults
}
// registry (all called only from the Core goroutine):
func newIndicatorSet() *indicatorSet
func (s *indicatorSet) ensure(c *Core, id string, spec IndicatorSpec)
func (s *indicatorSet) release(id string)
func (s *indicatorSet) onBar(c *Core, b Bar)
// calculators:
type calc interface {
	slots() []string
	fold(b Bar)
	points(b Bar) []slotPoint
}
type slotPoint struct {
	slot  string
	value float64
	ok    bool // false while warming up — no point emitted
}
func newCalc(spec IndicatorSpec) (calc, error)
```

**Semantics:**
1. **ensure:** existing id → refcount++ and re-emit the stored snapshot (a new subscriber needs the series). New id → build the calc (params validated against catalog bounds 1–400; invalid spec logs `slog.Warn` and creates nothing), seed by iterating `finalizedBars(symbol, tf)` — for each bar record `points` (pre-fold) as the final point, then `fold` — then emit one snapshot `IndicatorUpdate` per slot.
2. **onBar** for matching `(symbol, tf)` instances:
   - in-progress bar → emit one non-snapshot point per ok slot (computed from last-folded state; not stored).
   - finalized bar at a **new** bucket (`> lastFolded`) → emit final points, append to the stored series, `fold`.
   - finalized bar at an **old or repeated** bucket (deep-history insert rewriting the past) → recompute the whole instance from `finalizedBars` and re-emit snapshots — honest and simple; the O(history) cost only occurs on backfill.
3. **VWAP** is session-anchored to the full ET trading day (pre-market included — gap traders live before 09:30): cumulative Σ(typical·V)/ΣV with typical = (H+L+C)/3, resetting when the ET day of the bucket changes. Zero-volume warmup emits nothing.
4. **DELTA** = `BuyV − SellV` per bar; zero (honestly) where no tick data covers the bar. **VOLUME** = `V`. Both stateless.

- [ ] **Step 1: Write the failing tests**

`engine/internal/md/indicator_test.go`:

```go
package md

import (
	"math"
	"math/rand"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func mkBar(i int, c float64, v, buyV int64) Bar {
	return Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: t0Ms + int64(i)*60_000,
		O: c - 0.1, H: c + 0.2, L: c - 0.2, C: c, V: v, BuyV: buyV, SellV: v - buyV}
}

func TestEMAMatchesReference(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 3}}
	ca, err := newCalc(spec)
	if err != nil {
		t.Fatal(err)
	}
	closes := []float64{10, 11, 12, 13, 14}
	var got []float64
	for i, cl := range closes {
		b := mkBar(i, cl, 100, 60)
		for _, p := range ca.points(b) {
			if p.ok {
				got = append(got, p.value)
			}
		}
		ca.fold(b)
	}
	// period 3, alpha .5: seed SMA(10,11,12)=11; then .5*13+.5*11=12; .5*14+.5*12=13.
	want := []float64{11, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("points = %v, want %v", got, want)
		}
	}
}

func TestFormingPointsNeverCompound(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2}}
	pure, _ := newCalc(spec)
	noisy, _ := newCalc(spec)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		b := mkBar(i, 100+rng.Float64(), 100, 50)
		// The noisy calc previews many forming variants before the fold —
		// state must be unaffected.
		for j := 0; j < 5; j++ {
			forming := b
			forming.C += rng.Float64()
			noisy.points(forming)
		}
		pp, np := pure.points(b), noisy.points(b)
		if pp[0].value != np[0].value {
			t.Fatalf("bar %d: forming previews mutated state: %v vs %v", i, pp, np)
		}
		pure.fold(b)
		noisy.fold(b)
	}
}

// Streaming == batch: every type's streamed final point equals a fresh calc
// folding the prefix and previewing the bar.
func TestStreamingEqualsBatchRecompute(t *testing.T) {
	specs := []IndicatorSpec{
		{Type: IndVWAP}, {Type: IndSMA, Params: map[string]float64{"period": 5}},
		{Type: IndEMA, Params: map[string]float64{"period": 4}},
		{Type: IndMACD, Params: map[string]float64{"fast": 3, "slow": 6, "signal": 3}},
		{Type: IndVolume}, {Type: IndDelta},
	}
	rng := rand.New(rand.NewSource(7))
	var bars []Bar
	px := 100.0
	for i := 0; i < 120; i++ {
		px += rng.Float64() - 0.5
		v := int64(rng.Intn(1000) + 1)
		bars = append(bars, mkBar(i, px, v, v/2))
	}
	for _, spec := range specs {
		spec.Symbol, spec.TF = "US.AAPL", session.TF1m
		streaming, _ := newCalc(spec)
		for i, b := range bars {
			sp := streaming.points(b)
			batch, _ := newCalc(spec)
			for _, prev := range bars[:i] {
				batch.fold(prev)
			}
			bp := batch.points(b)
			for k := range sp {
				if sp[k].ok != bp[k].ok || (sp[k].ok && math.Abs(sp[k].value-bp[k].value) > 1e-9) {
					t.Fatalf("%s bar %d slot %s: streaming %+v != batch %+v", spec.Type, i, sp[k].slot, sp[k], bp[k])
				}
			}
			streaming.fold(b)
		}
	}
}

func TestVWAPResetsAtDayBoundary(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndVWAP}
	ca, _ := newCalc(spec)
	day1 := mkBar(0, 100, 100, 50)
	ca.fold(day1)
	day2 := day1
	day2.BucketMs += 86_400_000 // next ET day
	day2.C, day2.O, day2.H, day2.L = 200, 199.9, 200.2, 199.8
	pts := ca.points(day2)
	tp := (day2.H + day2.L + day2.C) / 3
	if !pts[0].ok || math.Abs(pts[0].value-tp) > 1e-9 {
		t.Fatalf("VWAP after day reset = %+v, want fresh %g", pts[0], tp)
	}
}

func TestIndicatorLifecycleThroughCore(t *testing.T) {
	c, drain := runCore(t)
	// Two finalized 1m bars, then an EMA(2) instance seeds from history.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100, 102, 100, 101, 400)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103, 101, 102, 300)}})
	c.EnsureIndicator("ema-1", IndicatorSpec{
		Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2},
	})
	// A live update to the forming bar streams a delta point.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103.5, 101, 103, 350)}})
	countEma := func(us []Update) (snaps, deltas int, lastSnap IndicatorUpdate) {
		for _, u := range us {
			if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "ema-1" {
				if iu.Snapshot {
					snaps++
					lastSnap = iu
				} else {
					deltas++
				}
			}
		}
		return
	}
	snaps, deltas, snap := countEma(drain())
	if snaps != 1 || snap.SeriesKey != "ema-1" || len(snap.Points) != 1 {
		t.Fatalf("snapshots=%d last=%+v, want 1 snapshot with 1 seeded point (bars 0-1 finalized; EMA(2) warm from bar 1)", snaps, snap)
	}
	if deltas == 0 {
		t.Fatal("no delta point for the forming-bar update")
	}
	c.ReleaseIndicator("ema-1")
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(3, 103, 104, 102, 103.5, 100)}})
	// drain() re-returns the full accumulated stream; ema-1 counts must be
	// frozen after the release.
	snaps2, deltas2, _ := countEma(drain())
	if snaps2 != snaps || deltas2 != deltas {
		t.Fatalf("released instance still emitting: snapshots %d->%d deltas %d->%d", snaps, snaps2, deltas, deltas2)
	}
}

func TestMACDSlotKeys(t *testing.T) {
	c, drain := runCore(t)
	for i := 0; i < 12; i++ {
		c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(i, 100+float64(i), 101+float64(i), 99+float64(i), 100.5+float64(i), 100)}})
	}
	c.EnsureIndicator("macd-1", IndicatorSpec{
		Symbol: "US.AAPL", TF: session.TF1m, Type: IndMACD,
		Params: map[string]float64{"fast": 3, "slow": 6, "signal": 3},
	})
	keys := map[string]bool{}
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "macd-1" && iu.Snapshot {
			keys[iu.SeriesKey] = true
		}
	}
	for _, want := range []string{"macd-1#macd", "macd-1#signal", "macd-1#hist"} {
		if !keys[want] {
			t.Fatalf("snapshot keys = %v, missing %s", keys, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/md/ -run 'TestEMA|TestForming|TestStreaming|TestVWAP|TestIndicatorLifecycle|TestMACD' -v`
Expected: FAIL — `newCalc` undefined / stub registry emits nothing.

- [ ] **Step 3: Implement**

`engine/internal/md/ind_calcs.go`:

```go
package md

import (
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// slotPoint is one slot's output for one bar. ok=false during warmup.
type slotPoint struct {
	slot  string
	value float64
	ok    bool
}

// calc is the streaming indicator contract. fold advances permanent state
// with a FINALIZED bar (O(1)); points computes output for any bar (forming
// or about-to-fold) from the last-folded state WITHOUT mutating anything —
// the forming bar's point is always recomputed from finalized state, so a
// live EMA never compounds partials. (go-engine-design §Indicators)
type calc interface {
	slots() []string
	fold(b Bar)
	points(b Bar) []slotPoint
}

func paramOr(p map[string]float64, key string, def float64) float64 {
	if v, ok := p[key]; ok {
		return v
	}
	return def
}

func intParam(p map[string]float64, key string, def float64) (int, error) {
	v := paramOr(p, key, def)
	n := int(v)
	if float64(n) != v || n < 1 || n > 400 {
		return 0, fmt.Errorf("md: indicator param %s=%v out of range [1,400]", key, v)
	}
	return n, nil
}

// newCalc builds a calculator; parameter names/defaults mirror the UI catalog
// (EMA period 9, SMA period 20, MACD 12/26/9).
func newCalc(spec IndicatorSpec) (calc, error) {
	switch spec.Type {
	case IndVWAP:
		return &vwapCalc{}, nil
	case IndSMA:
		n, err := intParam(spec.Params, "period", 20)
		if err != nil {
			return nil, err
		}
		return newSMACalc(n), nil
	case IndEMA:
		n, err := intParam(spec.Params, "period", 9)
		if err != nil {
			return nil, err
		}
		return &emaCalc{state: newEMAState(n)}, nil
	case IndMACD:
		fast, err := intParam(spec.Params, "fast", 12)
		if err != nil {
			return nil, err
		}
		slow, err := intParam(spec.Params, "slow", 26)
		if err != nil {
			return nil, err
		}
		sig, err := intParam(spec.Params, "signal", 9)
		if err != nil {
			return nil, err
		}
		return &macdCalc{fast: newEMAState(fast), slow: newEMAState(slow), sig: newEMAState(sig)}, nil
	case IndVolume:
		return statelessCalc(func(b Bar) float64 { return float64(b.V) }), nil
	case IndDelta:
		return statelessCalc(func(b Bar) float64 { return float64(b.BuyV - b.SellV) }), nil
	}
	return nil, fmt.Errorf("md: unknown indicator type %q", spec.Type)
}

// ---- stateless (VOLUME, DELTA) ----

type statelessCalc func(Bar) float64

func (statelessCalc) slots() []string { return []string{"hist"} }
func (statelessCalc) fold(Bar)        {}
func (f statelessCalc) points(b Bar) []slotPoint {
	return []slotPoint{{slot: "hist", value: f(b), ok: true}}
}

// ---- VWAP (session-anchored: resets each ET trading day; pre-market included) ----

type vwapCalc struct {
	day   int64
	cumPV float64
	cumV  float64
}

func (*vwapCalc) slots() []string { return []string{"line"} }

func typical(b Bar) float64 { return (b.H + b.L + b.C) / 3 }

func (v *vwapCalc) fold(b Bar) {
	if d := session.BucketStartMs(b.BucketMs, session.TFDay); d != v.day {
		v.day, v.cumPV, v.cumV = d, 0, 0
	}
	v.cumPV += typical(b) * float64(b.V)
	v.cumV += float64(b.V)
}

func (v *vwapCalc) points(b Bar) []slotPoint {
	pv, vol := v.cumPV, v.cumV
	if session.BucketStartMs(b.BucketMs, session.TFDay) != v.day {
		pv, vol = 0, 0 // bar opens a new day: preview a fresh session
	}
	pv += typical(b) * float64(b.V)
	vol += float64(b.V)
	if vol == 0 {
		return []slotPoint{{slot: "line"}}
	}
	return []slotPoint{{slot: "line", value: pv / vol, ok: true}}
}

// ---- SMA ----

type smaCalc struct {
	period int
	win    []float64 // last period-1 finalized closes
	sum    float64
}

func newSMACalc(period int) *smaCalc { return &smaCalc{period: period} }

func (*smaCalc) slots() []string { return []string{"line"} }

func (s *smaCalc) fold(b Bar) {
	s.win = append(s.win, b.C)
	s.sum += b.C
	if len(s.win) >= s.period { // keep exactly period-1 for the preview window
		s.sum -= s.win[0]
		s.win = s.win[1:]
	}
}

func (s *smaCalc) points(b Bar) []slotPoint {
	if len(s.win) < s.period-1 {
		return []slotPoint{{slot: "line"}}
	}
	return []slotPoint{{slot: "line", value: (s.sum + b.C) / float64(s.period), ok: true}}
}

// ---- EMA (seeded with the SMA of the first `period` closes) ----

type emaState struct {
	period int
	alpha  float64
	count  int
	seed   float64
	val    float64
}

func newEMAState(period int) *emaState {
	return &emaState{period: period, alpha: 2 / float64(period+1)}
}

func (e *emaState) fold(v float64) {
	e.count++
	switch {
	case e.count < e.period:
		e.seed += v
	case e.count == e.period:
		e.val = (e.seed + v) / float64(e.period)
	default:
		e.val = e.alpha*v + (1-e.alpha)*e.val
	}
}

// preview computes the EMA as if v were folded, without folding it.
func (e *emaState) preview(v float64) (float64, bool) {
	switch {
	case e.count+1 < e.period:
		return 0, false
	case e.count+1 == e.period:
		return (e.seed + v) / float64(e.period), true
	default:
		return e.alpha*v + (1-e.alpha)*e.val, true
	}
}

type emaCalc struct{ state *emaState }

func (*emaCalc) slots() []string { return []string{"line"} }
func (e *emaCalc) fold(b Bar)    { e.state.fold(b.C) }
func (e *emaCalc) points(b Bar) []slotPoint {
	v, ok := e.state.preview(b.C)
	return []slotPoint{{slot: "line", value: v, ok: ok}}
}

// ---- MACD (fast/slow EMAs of close; signal EMA of the macd line) ----

type macdCalc struct {
	fast, slow, sig *emaState
}

func (*macdCalc) slots() []string { return []string{"macd", "signal", "hist"} }

func (m *macdCalc) fold(b Bar) {
	m.fast.fold(b.C)
	m.slow.fold(b.C)
	if m.fast.count >= m.fast.period && m.slow.count >= m.slow.period {
		m.sig.fold(m.fast.val - m.slow.val)
	}
}

func (m *macdCalc) points(b Bar) []slotPoint {
	fv, fok := m.fast.preview(b.C)
	sv, sok := m.slow.preview(b.C)
	out := []slotPoint{{slot: "macd"}, {slot: "signal"}, {slot: "hist"}}
	if !fok || !sok {
		return out
	}
	macd := fv - sv
	out[0] = slotPoint{slot: "macd", value: macd, ok: true}
	if sigv, sigok := m.sig.preview(macd); sigok {
		out[1] = slotPoint{slot: "signal", value: sigv, ok: true}
		out[2] = slotPoint{slot: "hist", value: macd - sigv, ok: true}
	}
	return out
}
```

`engine/internal/md/indicator.go` (replaces the Task 9 stub):

```go
package md

import (
	"log/slog"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// IndicatorType names the v1 catalog. Values match the UI contract.
type IndicatorType string

const (
	IndVWAP   IndicatorType = "VWAP"
	IndEMA    IndicatorType = "EMA"
	IndSMA    IndicatorType = "SMA"
	IndMACD   IndicatorType = "MACD"
	IndVolume IndicatorType = "VOLUME"
	IndDelta  IndicatorType = "DELTA"
)

// IndicatorSpec identifies what an instance computes.
type IndicatorSpec struct {
	Symbol string
	TF     session.Timeframe
	Type   IndicatorType
	Params map[string]float64
}

type symTF struct {
	symbol string
	tf     session.Timeframe
}

type instance struct {
	id         string
	spec       IndicatorSpec
	c          calc
	refs       int
	lastFolded int64
	series     map[string][]Point // stored FINAL points per slot (snapshot source)
}

// seriesKey follows the UI contract: single-slot instances stream under the
// instanceId itself; multi-slot ones under "instanceId#slot".
func (in *instance) seriesKey(slot string) string {
	if len(in.c.slots()) == 1 {
		return in.id
	}
	return in.id + "#" + slot
}

// indicatorSet is the per-core instance registry. All methods run on the
// Core goroutine.
type indicatorSet struct {
	byID    map[string]*instance
	bySymTF map[symTF][]*instance
}

func newIndicatorSet() *indicatorSet {
	return &indicatorSet{byID: make(map[string]*instance), bySymTF: make(map[symTF][]*instance)}
}

func (s *indicatorSet) ensure(c *Core, id string, spec IndicatorSpec) {
	if in, ok := s.byID[id]; ok {
		in.refs++
		s.emitSnapshots(c, in) // the new subscriber needs the series
		return
	}
	ca, err := newCalc(spec)
	if err != nil {
		slog.Warn("indicator spec rejected", "id", id, "type", spec.Type, "err", err)
		return
	}
	in := &instance{id: id, spec: spec, c: ca, refs: 1, lastFolded: -1,
		series: make(map[string][]Point)}
	s.byID[id] = in
	key := symTF{symbol: spec.Symbol, tf: spec.TF}
	s.bySymTF[key] = append(s.bySymTF[key], in)
	s.reseed(c, in)
}

// reseed rebuilds an instance from finalized history and emits snapshots.
func (s *indicatorSet) reseed(c *Core, in *instance) {
	ca, err := newCalc(in.spec)
	if err != nil { // cannot happen after ensure validated once; stay safe
		return
	}
	in.c = ca
	in.lastFolded = -1
	in.series = make(map[string][]Point)
	for _, b := range c.bars.finalizedBars(in.spec.Symbol, in.spec.TF) {
		s.foldFinal(in, b)
	}
	s.emitSnapshots(c, in)
}

// foldFinal records the final points for b, then folds it.
func (s *indicatorSet) foldFinal(in *instance, b Bar) {
	for _, p := range in.c.points(b) {
		if p.ok {
			in.series[p.slot] = append(in.series[p.slot], Point{TimeMs: b.BucketMs, Value: p.value})
		}
	}
	in.c.fold(b)
	in.lastFolded = b.BucketMs
}

func (s *indicatorSet) emitSnapshots(c *Core, in *instance) {
	for _, slot := range in.c.slots() {
		pts := in.series[slot]
		c.emit(IndicatorUpdate{
			InstanceID: in.id,
			SeriesKey:  in.seriesKey(slot),
			Points:     append([]Point(nil), pts...),
			Snapshot:   true,
		})
	}
}

func (s *indicatorSet) release(id string) {
	in, ok := s.byID[id]
	if !ok {
		return
	}
	in.refs--
	if in.refs > 0 {
		return
	}
	delete(s.byID, id)
	key := symTF{symbol: in.spec.Symbol, tf: in.spec.TF}
	list := s.bySymTF[key]
	for i, x := range list {
		if x == in {
			s.bySymTF[key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(s.bySymTF[key]) == 0 {
		delete(s.bySymTF, key)
	}
}

// onBar routes a bar emission to matching instances (called from barOut).
func (s *indicatorSet) onBar(c *Core, b Bar) {
	list := s.bySymTF[symTF{symbol: b.Symbol, tf: b.TF}]
	for _, in := range list {
		switch {
		case b.InProgress:
			for _, p := range in.c.points(b) {
				if p.ok {
					c.emit(IndicatorUpdate{
						InstanceID: in.id, SeriesKey: in.seriesKey(p.slot),
						Points: []Point{{TimeMs: b.BucketMs, Value: p.value}},
					})
				}
			}
		case b.BucketMs > in.lastFolded:
			for _, p := range in.c.points(b) {
				if p.ok {
					in.series[p.slot] = append(in.series[p.slot], Point{TimeMs: b.BucketMs, Value: p.value})
					c.emit(IndicatorUpdate{
						InstanceID: in.id, SeriesKey: in.seriesKey(p.slot),
						Points: []Point{{TimeMs: b.BucketMs, Value: p.value}},
					})
				}
			}
			in.c.fold(b)
			in.lastFolded = b.BucketMs
		default:
			// A finalized bar rewrote the past (deep-history insert):
			// recompute from scratch and re-snapshot. Only backfill pays this.
			s.reseed(c, in)
		}
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test -race ./internal/md/ -v`
Expected: PASS — including the streaming==batch property across all six types.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/md/indicator.go internal/md/ind_calcs.go internal/md/indicator_test.go
git commit -m "feat(engine/md): v1 indicators (VWAP/EMA/SMA/MACD/VOLUME/DELTA) - streaming contract, refcounted instances"
```

---

## Task 13: Determinism — `replay(events) == state`

The engine-design invariant as an executable test: the same event sequence always produces the same updates, and chunking (how ticks are batched into events) cannot change final state. This is what makes Plan 3's journal replay trustworthy.

**Files:**
- Test: `engine/internal/md/determinism_test.go`

**Interfaces:**
- Consumes: everything from Tasks 9–12. Produces no new API.

- [ ] **Step 1: Write the test (it should pass immediately — it exists to catch regressions and any hidden nondeterminism such as map-order dependence)**

`engine/internal/md/determinism_test.go`:

```go
package md

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// script builds a deterministic mixed-event day: seed bars, seed ticks, live
// pushes for two symbols, a resync, indicator lifecycle.
func script() []feed.Event {
	rng := rand.New(rand.NewSource(99))
	var evs []feed.Event
	mk := func(sym string, seq int64, offMs int64, px float64, v int64, d feed.Direction) feed.Tick {
		return feed.Tick{Symbol: sym, Seq: seq, TsMs: t0Ms + offMs, Price: px, Volume: v, Dir: d}
	}
	// Cache seeds.
	evs = append(evs, feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: t0Ms - 120_000, O: 99, H: 99.5, L: 98.9, C: 99.2, Volume: 800},
		{Symbol: "US.AAPL", BucketMs: t0Ms - 60_000, O: 99.2, H: 100, L: 99.1, C: 99.9, Volume: 900},
	}})
	seq := int64(0)
	px := 100.0
	dirs := []feed.Direction{feed.Buy, feed.Sell, feed.Neutral}
	var batch []feed.Tick
	for off := int64(0); off < 300_000; off += 1_000 + int64(rng.Intn(4000)) {
		seq++
		px += rng.Float64() - 0.5
		batch = append(batch, mk("US.AAPL", seq, off, px, int64(rng.Intn(500)+1), dirs[rng.Intn(3)]))
		if len(batch) == 3 {
			evs = append(evs, feed.TicksEvent{Ticks: batch})
			batch = nil
		}
	}
	if len(batch) > 0 {
		evs = append(evs, feed.TicksEvent{Ticks: batch})
	}
	evs = append(evs,
		feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", TsMs: t0Ms + 100_000, Last: px}},
		feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", TsMs: t0Ms + 100_000,
			Bids: []feed.BookLevel{{Price: px - 0.01, Volume: 300}},
			Asks: []feed.BookLevel{{Price: px + 0.01, Volume: 200}}}},
		feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
		feed.ConnDownEvent{}, feed.ConnUpEvent{}, feed.ResyncedEvent{},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
	)
	return evs
}

// run feeds the script through a fresh core and returns every update, in order.
func run(t *testing.T, evs []feed.Event) []Update {
	t.Helper()
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	// Collect concurrently so the 8192-cap updates channel never overflows
	// (an overflow would DROP updates and break determinism-by-count).
	var got []Update
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-done:
				for { // core stopped: drain whatever is still buffered
					select {
					case u := <-c.Updates():
						got = append(got, u)
					default:
						return
					}
				}
			}
		}
	}()
	c.EnsureIndicator("vwap-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF10s, Type: IndVWAP})
	for _, ev := range evs {
		c.Feed(ev)
	}
	c.EnsureIndicator("ema-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA,
		Params: map[string]float64{"period": 2}})
	// Let the single writer finish the inbox, then stop and drain.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	<-collected
	return got
}

func TestReplayProducesIdenticalUpdates(t *testing.T) {
	evs := script()
	a := run(t, evs)
	b := run(t, evs)
	if len(a) != len(b) {
		t.Fatalf("update counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("update %d differs:\n%#v\n%#v", i, a[i], b[i])
		}
	}
}

// Chunking invariance: re-batching the same ticks into different event sizes
// must not change any FINALIZED bar.
func TestChunkingCannotChangeFinalBars(t *testing.T) {
	evs := script()
	rechunked := make([]feed.Event, 0, len(evs))
	for _, ev := range evs {
		if te, ok := ev.(feed.TicksEvent); ok && !te.Seed {
			for _, tk := range te.Ticks { // one event per tick
				rechunked = append(rechunked, feed.TicksEvent{Ticks: []feed.Tick{tk}})
			}
			continue
		}
		rechunked = append(rechunked, ev)
	}
	finals := func(us []Update) map[string]Bar {
		out := make(map[string]Bar)
		for _, u := range us {
			if bu, ok := u.(BarUpdate); ok && !bu.Bar.InProgress {
				out[fmt.Sprintf("%s/%s/%d", bu.Bar.Symbol, bu.Bar.TF, bu.Bar.BucketMs)] = bu.Bar
			}
		}
		return out
	}
	a := finals(run(t, evs))
	b := finals(run(t, rechunked))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("finalized bars diverge under re-chunking:\n%d bars vs %d bars", len(a), len(b))
	}
}
```

> **If either test fails, the bug is in Tasks 9–12** — the usual suspects are map iteration leaking into emission order (sort before emitting), hidden wall-clock reads, or watermark state that depends on batch boundaries. Fix the source, never loosen the test. The `time.Sleep` drain is acceptable here because determinism is about *content*, not timing; if it flakes, replace with an inbox-empty check (add a `len(c.inbox)==0` spin), not a longer sleep.

- [ ] **Step 2: Run it**

Run: `cd engine && go test -race ./internal/md/ -run 'TestReplay|TestChunking' -v -count=5`
Expected: PASS, 5/5 — `-count=5` shakes out map-order nondeterminism.

- [ ] **Step 3: Commit**

```bash
cd engine
git add internal/md/determinism_test.go
git commit -m "test(engine/md): replay(events)==state - identical updates, chunking-invariant final bars"
```

## Task 14: Config growth + `cmd/etape` harness — the live deliverable

Wire client → OpenDFeed → md.Core and prove the whole plan against live OpenD: subscribe a watchlist, watch 10s/1m bars finalize with buy/sell delta, survive an OpenD restart with a visible resync.

**Files:**
- Modify: `engine/internal/config/config.go`, `engine/internal/config/config_test.go`
- Modify: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: everything.
- Produces: config sections consumed again by Plans 3/6:

```toml
# ~/.eTape/config.toml (all keys optional; defaults shown)
[opend]
host = "127.0.0.1"
port = 11111

[feed]
watchlist = []                # symbols pre-subscribed at boot, watch profile (e.g. ["US.AAPL"])
extended_time = true          # US pre/post on every subscription
unsub_hysteresis_secs = 300   # delayed unsubscribe after release
quota_slots = 100             # subscription budget (base tier)

[md]
tape_ring = 65536             # per-symbol tick ring
session_anchor = "09:30"      # intraday bucket anchor, ET
```

- [ ] **Step 1: Write the failing config test**

Append to `engine/internal/config/config_test.go`:

```go
func TestFeedAndMDSectionsWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[feed]
watchlist = ["US.AAPL", "US.TSLA"]
quota_slots = 300

[md]
session_anchor = "09:00"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Feed.Watchlist) != 2 || cfg.Feed.QuotaSlots != 300 {
		t.Fatalf("feed = %+v", cfg.Feed)
	}
	if !cfg.Feed.ExtendedTime || cfg.Feed.UnsubHysteresisSecs != 300 {
		t.Fatalf("feed defaults not preserved: %+v", cfg.Feed)
	}
	if cfg.MD.TapeRing != 65536 {
		t.Fatalf("md defaults not preserved: %+v", cfg.MD)
	}
	secs, err := cfg.MD.AnchorSecs()
	if err != nil || secs != 9*3600 {
		t.Fatalf("AnchorSecs = %d, %v; want 32400", secs, err)
	}
}

func TestAnchorSecsRejectsGarbage(t *testing.T) {
	m := MD{SessionAnchor: "9am"}
	if _, err := m.AnchorSecs(); err == nil {
		t.Fatal("want parse error for '9am'")
	}
}
```

- [ ] **Step 2: Run to verify failure, then implement**

Run: `cd engine && go test ./internal/config/ -v` → FAIL (`cfg.Feed` undefined).

Extend `engine/internal/config/config.go`:

```go
// Feed configures the market-data feed adapter.
type Feed struct {
	Watchlist           []string `toml:"watchlist"`
	ExtendedTime        bool     `toml:"extended_time"`
	UnsubHysteresisSecs int      `toml:"unsub_hysteresis_secs"`
	QuotaSlots          int      `toml:"quota_slots"`
}

// MD configures the market-data core.
type MD struct {
	TapeRing      int    `toml:"tape_ring"`
	SessionAnchor string `toml:"session_anchor"` // "HH:MM" ET
}

// AnchorSecs parses SessionAnchor into seconds-since-ET-midnight.
func (m MD) AnchorSecs() (int64, error) {
	t, err := time.Parse("15:04", m.SessionAnchor)
	if err != nil {
		return 0, fmt.Errorf("config: session_anchor %q must be HH:MM: %w", m.SessionAnchor, err)
	}
	return int64(t.Hour())*3600 + int64(t.Minute())*60, nil
}
```

Add `Feed Feed` + `MD MD` fields to `Config` (tags `feed`, `md`) and extend `Default()`:

```go
func Default() Config {
	return Config{
		OpenD: OpenD{Host: "127.0.0.1", Port: 11111},
		Feed:  Feed{ExtendedTime: true, UnsubHysteresisSecs: 300, QuotaSlots: 100},
		MD:    MD{TapeRing: 65536, SessionAnchor: "09:30"},
	}
}
```

(`toml.DecodeFile` merges over the defaults, so absent keys keep them — the existing `Load` needs no change. Add `"time"` to imports.)

- [ ] **Step 3: Rewrite the harness main**

`engine/cmd/etape/main.go`:

```go
// Command etape is the eTape engine. In this plan it is the market-data
// harness: connect OpenD → feed adapter → md core, subscribe the watchlist
// (+ --focus symbols with depth), and log what the core emits. Plan 6
// replaces main with the full boot sequence (store → uihub → OpenD → exec).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	watch := flag.String("watch", "", "comma-separated symbols to watch (adds to config watchlist)")
	focus := flag.String("focus", "", "comma-separated symbols to focus (adds depth + quote)")
	verbose := flag.Bool("v", false, "log quotes/books/tape (noisy)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
	fd := opend.NewOpenDFeed(client, opend.FeedOptions{
		Budget:              cfg.Feed.QuotaSlots,
		Hysteresis:          time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
		DisableExtendedTime: !cfg.Feed.ExtendedTime,
	})
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})

	go func() { _ = client.Run(ctx) }()
	go func() { _ = fd.Run(ctx) }()
	go func() { _ = core.Run(ctx) }()
	go func() { // the feed→core pipe; Plan 3's journal tee slots in here
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-fd.Events():
				core.Feed(ev)
			}
		}
	}()

	// Demands: config watchlist + --watch as watch profile, --focus focused.
	seen := 0
	for _, s := range append(cfg.Feed.Watchlist, splitCSV(*watch)...) {
		fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		seen++
	}
	var firstFocus string
	for _, s := range splitCSV(*focus) {
		fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		if firstFocus == "" {
			firstFocus = s
		}
		seen++
	}
	if seen == 0 {
		log.Warn("no symbols demanded; pass --watch/--focus or set [feed].watchlist")
	}
	if firstFocus != "" { // prove the indicator pipeline end-to-end
		core.EnsureIndicator("harness-vwap", md.IndicatorSpec{Symbol: firstFocus, TF: session.TF1m, Type: md.IndVWAP})
		core.EnsureIndicator("harness-ema9", md.IndicatorSpec{Symbol: firstFocus, TF: session.TF1m, Type: md.IndEMA,
			Params: map[string]float64{"period": 9}})
	}

	go func() { // drain marks (exec's input in Plan 4)
		for {
			select {
			case <-ctx.Done():
				return
			case <-core.Marks():
			}
		}
	}()

	log.Info("engine up", "opend", cfg.OpenD.Addr(), "anchor", cfg.MD.SessionAnchor)
	for {
		select {
		case <-ctx.Done():
			log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates())
			return
		case u := <-core.Updates():
			switch v := u.(type) {
			case md.BarUpdate:
				if !v.Bar.InProgress {
					log.Info("bar", "sym", v.Bar.Symbol, "tf", v.Bar.TF, "bucket", v.Bar.BucketMs,
						"o", v.Bar.O, "h", v.Bar.H, "l", v.Bar.L, "c", v.Bar.C,
						"v", v.Bar.V, "delta", v.Bar.BuyV-v.Bar.SellV, "ticks", v.Bar.Ticks, "gap", v.Bar.Gap)
				}
			case md.IndicatorUpdate:
				if v.Snapshot {
					log.Info("indicator snapshot", "id", v.InstanceID, "key", v.SeriesKey, "points", len(v.Points))
				}
			case md.MismatchUpdate:
				log.Warn("1m mismatch", "sym", v.Symbol, "bucket", v.BucketMs, "detail", v.Detail)
			case md.ConnUpdate:
				log.Info("feed connection", "up", v.Up)
			case md.ResyncedUpdate:
				log.Info("feed resynced")
			case md.QuoteUpdate:
				if *verbose {
					log.Info("quote", "sym", v.Quote.Symbol, "last", v.Quote.Last)
				}
			case md.BookUpdate:
				if *verbose {
					log.Info("book", "sym", v.Book.Symbol, "bids", len(v.Book.Bids), "asks", len(v.Book.Asks))
				}
			case md.TapeUpdate:
				if *verbose {
					log.Info("tape", "sym", v.Symbol, "ticks", len(v.Ticks))
				}
			}
		}
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

(Plan 1's `errors`-based exit-code handling of `client.Run` is gone deliberately — the run moved into a supervised goroutine and `context.Canceled` is the normal shutdown path.)

- [ ] **Step 4: Full gates + live smoke**

Run: `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run`
Expected: all green.

**Live smoke (requires OpenD running; markets matter):**

```bash
cd engine && go run ./cmd/etape --focus US.AAPL -v        # Mon 04:00+ ET: pre-market ticks
go run ./cmd/etape --watch HK.00700 -v                    # HK session (LV1: ticker/quote/K1m, 1-level book)
go run ./cmd/etape --watch CC.BTC -v                      # weekends: crypto trades 24/7
```

Expected (verify each):
1. `feed connection up=true`, then seed events land — with `-v`, an immediate tape burst (≤1,000 cached ticks) and 1m bars.
2. 10s `bar` lines with sane OHLC continuity and `delta` — same shape as `prototypes/tick_to_10s_bars.py` output.
3. `indicator snapshot` lines for `harness-vwap` and `harness-ema9` (focus run).
4. Kill OpenD (or its network) mid-run: `feed connection up=false` → on recovery `up=true` → seeds → `feed resynced`, and the next 10s bar logs `gap=true`.
5. Clean Ctrl-C: `shutdown complete droppedUpdates=0`.

If the 1m `mismatch` warning fires persistently with volume-only detail, that's the K_1M-vs-tick odd-lot difference — record magnitudes for Monday tuning; do not chase it in this plan.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/config/ cmd/etape/main.go
git commit -m "feat(engine): market-data harness - config [feed]/[md], client->feed->core wiring, live smoke"
```

---

## Task 15: Golden corpus extension — real Qot frames (live-dependent)

Extend Plan 1's capture harness to the Plan 2 protocol surface so the decoders are validated against real OpenD bytes, not just synthetic protobufs. **Requires live OpenD**; push captures additionally need a market producing data (weekend: `CC.BTC`; else Monday pre-market `US.*`). This task can run any time after Task 5 — it is last only because it must not block the deterministic work.

**Files:**
- Modify: `engine/scripts/capture_golden_frames.py`
- Modify: `engine/internal/feed/opend/golden_test.go`
- Create: `engine/internal/feed/opend/testdata/golden/qot_*.jsonl` (captured), update `manifest.json`

**Interfaces:** none new — extends the existing JSONL fixture format (`{"proto_id","direction","is_push","proto_fmt_type","proto_ver","serial_no","body_len","body_sha1_hex","frame_hex"}` per line, per Plan 1).

- [ ] **Step 1: Extend the capture script**

Add a `--qot SYMBOL --secs N` mode to `engine/scripts/capture_golden_frames.py` (same `NetManager.send` / `open_context_base.parse_rsp` hooks as Plan 1 — they already capture every c2s/s2c pair):

```python
def capture_qot(symbol: str, secs: int) -> None:
    """Subscribe QUOTE/ORDER_BOOK/TICKER/K_1M on symbol, let pushes flow for
    secs, and exercise every Plan 2 request once. The global hooks record
    all frames; Qot frames carry no account data (public-repo safe)."""
    from moomoo import OpenQuoteContext, SubType, KLType, AuType, RET_OK
    import datetime as dt
    ctx = OpenQuoteContext(host=HOST, port=PORT)
    try:
        ret, err = ctx.subscribe([symbol],
                                 [SubType.QUOTE, SubType.ORDER_BOOK, SubType.TICKER, SubType.K_1M],
                                 subscribe_push=True, extended_time=True)
        assert ret == RET_OK, f"subscribe failed: {err}"
        time.sleep(secs)                                    # pushes accumulate
        ctx.get_stock_quote([symbol])                       # 3004
        ctx.get_cur_kline(symbol, 100, ktype=KLType.K_1M, autype=AuType.QFQ)   # 3006
        ctx.get_rt_ticker(symbol, 100)                      # 3010
        ctx.get_order_book(symbol, num=10)                  # 3012
        today = dt.date.today().isoformat()
        ctx.request_history_kline(symbol, start=today, end=today,
                                  ktype=KLType.K_1M, autype=AuType.QFQ, max_count=100)  # 3103
    finally:
        ctx.close()
```

Route captured frames into per-protocol files (`qot_sub.jsonl`, `qot_getbasicqot.jsonl`, `qot_getkl.jsonl`, `qot_getticker.jsonl`, `qot_getorderbook.jsonl`, `qot_requesthistorykl.jsonl`, `qot_update_basicqot.jsonl`, `qot_update_ticker.jsonl`, `qot_update_orderbook.jsonl`, `qot_update_kl.jsonl`) using the existing protoID→filename mechanism; keep the 1001/1002 exclusion exactly as shipped. Update `manifest.json` (capture date, symbol, market session).

Run it while OpenD is up:

```bash
cd engine && python3 scripts/capture_golden_frames.py --qot CC.BTC --secs 30   # weekend
# Monday pre-market, richer: --qot US.AAPL --secs 30
```

If no pushes flow (market idle), commit the request/response captures anyway and re-run for `qot_update_*` when a session is live — the golden test iterates whatever files exist.

- [ ] **Step 2: Extend the golden test**

In `engine/internal/feed/opend/golden_test.go`, extend the existing table runner: for any golden file whose frame protoID is a push (`IsPushProtoID`), require `DecodePush` to return no error and at least zero events; for `qot_getkl.jsonl`/`qot_requesthistorykl.jsonl` s2c frames, decode via the pb type and assert every K-line's `decodeKLine(..., feed.Res1m)` succeeds and its `BucketMs` lands on a 60 000 ms boundary in ET (this pins the end-label normalization to real bytes — the strongest check this plan has for the 2026-07-05 finding):

```go
func TestGoldenQotKLinesNormalizeToMinuteBuckets(t *testing.T) {
	for _, f := range goldenFrames(t, "qot_getkl.jsonl", "qot_requesthistorykl.jsonl") {
		if f.direction != "s2c" {
			continue
		}
		var resp qotgetkl.Response // qotrequesthistorykl.Response for the second file
		if err := proto.Unmarshal(f.body, &resp); err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		for _, k := range resp.GetS2C().GetKlList() {
			bar, err := decodeKLine("US.TEST", k, feed.Res1m)
			if err != nil {
				t.Fatalf("%s: %v", f.name, err)
			}
			if bar.BucketMs%60_000 != 0 {
				t.Fatalf("%s: bucket %d not minute-aligned", f.name, bar.BucketMs)
			}
		}
	}
}
```

(Adapt to Plan 1's actual `goldenFrames` helper shape — the existing golden test file defines the JSONL loader; extend, don't duplicate. Skip cleanly with `t.Skip` when the files don't exist yet, so CI stays green before the capture runs.)

- [ ] **Step 3: Run the full gates**

Run: `cd engine && go test -race ./internal/feed/opend/ -v && golangci-lint run`
Expected: PASS (golden qot tests run when fixtures exist, skip otherwise).

- [ ] **Step 4: Sensitive sweep, then commit**

```bash
cd engine
grep -ril 'loginUserID\|connAESKey' internal/feed/opend/testdata/golden/ && echo "PII FOUND - DO NOT COMMIT" || echo clean
git add scripts/capture_golden_frames.py internal/feed/opend/golden_test.go internal/feed/opend/testdata/golden/
git commit -m "test(engine/opend): golden Qot corpus - real subscribe/push/backfill frames validate decoders"
```

---

## Execution notes

- **Branch:** implement on `worktree-engine-foundation-opend` (worktree `.claude/worktrees/engine-foundation-opend`) or a branch cut from it — Plan 1 lives there un-merged; main's checkout is owned by the concurrent UI session.
- **Task order is dependency order.** 1→8 build the adapter stack, 9→13 the core, 14 wires, 15 is live-dependent and can float anywhere after Task 5.
- **Live OpenD is needed only for Tasks 14 (smoke) and 15 (capture).** Everything else runs against the in-process mock. Weekend note (plan written Sunday 2026-07-05): US markets closed — use `CC.BTC` (24/7) for live pushes until Monday pre-market (04:00 ET); cache/history reads work any time OpenD is up.
- **Monday live-session verifications feeding this plan's constants** (add to the existing Monday checklist): K_1M push bucket labeling on live pushes (golden test pins the cache/history paths; pushes assumed identical — verify), mismatch-threshold magnitudes (`mismatchVolPct/Abs`), push cadences for the uihub coalescing defaults (Plan 6), observed push serial ranges (regression evidence for Task 1).
- **Known deferred hardening** (Plan 1 addendum backlog, still open, not this plan): send() write deadline, bodyLen cap before alloc, backoff reset on ConnUp, initial ConnDown emit. Plan 3 additionally gets: journal tee at the feed→core pipe (the harness comment marks the spot), `FakeClock`-driven subscription-manager soak tests, in-memory bar-series growth across multi-day runs (bounded per-day; store/prune arrives with SQLite).
- **CI gates at every task boundary:** `go build ./... && go vet ./... && go test -race ./... && golangci-lint run` from `engine/`.







