# UI Plan 6: E2E, Packaging & Polish — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **Execute tasks in the order written** — the ordering encodes real dependencies (e.g. the smoke E2E in Task 8 needs the venue-arm control from Task 5).

**Goal:** Ship v1 of the eTape UI — validated end-to-end against the real Go engine (Playwright over a replay-mode boot serving `ui/dist`), consuming the tygo-generated wire contract, with the accumulated polish gaps from Plans 2–5 closed.

**Architecture:** One deterministic **synthetic journal** drives everything. A new `cmd/genjournal` Go tool writes a scripted trading day (quotes/book/ticks/1m-bars for `US.AAPL` + `US.NVDA`) into a SQLite journal. The real `etape` binary replays it (`-replay <day> -speed 0 -replay-hold`) while serving the built `ui/dist` at `/` alongside `/ws`. Because the uihub mirror retains the full per-topic state (whole bar series, latest book/quote, last N ticks), replaying the entire day instantly and then holding the process open gives Playwright a stable, fully-populated snapshot with **zero timing races**. That same replay boot is also the source of truth for a re-captured combined fixture. Two engine/UI gaps that only surface against the real gate are closed here: replayed marks are fed to the sim broker (so submitted orders fill), and the UI gains a per-venue arm control (so orders pass the two-layer gate).

**Tech Stack:** Go 1.22+ (engine, `cmd/genjournal`, `internal/broker/sim`, `cmd/etape`), TypeScript + React + Vite + Vitest (UI), Playwright (new; E2E), `ws` (already a devDep; fixture capture), tygo (already wired; drift gate `make gen-ts-check`).

## Global Constraints

- **US stocks only**; symbols use the `US.<TICKER>` convention (e.g. `US.AAPL`).
- **High-frequency data never flows through React state** — chart/ladder/tape are canvas surfaces painted imperatively, coalesced to one repaint per rAF tick. Do not move md/book/tape into `useState`.
- **`ui/src/render/palette.ts` is the single color source of truth.** Chrome/panels consume it via `useTheme()`; painters take the palette in their paint state. No new hardcoded hex. `useTheme()` **throws** without an ancestor `<ThemeProvider>` — any test rendering a component that newly calls `useTheme()` must wrap the render in `<ThemeProvider>`.
- **Wire field names are camelCase and load-bearing.** `ui/src/gen/wsmsg.ts` (tygo-generated from `engine/internal/uihub/wsmsg`) is the source of truth; the drift gate `make gen-ts-check` (run from `engine/`) must stay green. Never hand-edit `gen/wsmsg.ts`.
- **TS strict mode** with `exactOptionalPropertyTypes` — never assign `undefined` to an optional property; omit the key instead.
- **Vitest pools:** any test file that mounts a real `<canvas>` (golden tests, `LadderPanel`/`TapePanel`) runs in the `forks` pool via `poolMatchGlobs` in `ui/vitest.config.ts`. New canvas-touching test files must be added there. (None in this plan do.)
- **No CI exists.** Tests + goldens + E2E are canonical on Earl's mac. The Playwright suite is a local `npm run e2e`, not a gated check.
- **Commits:** frequent, one per task. **Never** add a `Co-Authored-By:` / "Generated with" trailer to commit messages (Earl's global rule).
- **Two-layer gate:** an order passes only when **both** master **and** the target venue are armed (`engine/internal/exec/gate.go:72-78`). The UI must be able to arm both.
- **Order-ticket submit reality:** the ticket defaults to `type=LIMIT` with an empty price, which `preCheck` rejects *client-side* before any command reaches the wire (`ui/src/chrome/exec/preChecks.ts`). Any flow (E2E or otherwise) that must actually submit sets `type=MARKET` (marketable, no price needed).

---

## File Structure

**Engine (Go) — new:** `engine/cmd/genjournal/main.go` (+ `main_test.go`).
**Engine (Go) — modified:** `engine/cmd/etape/main.go` (`markBridge` sim-mark sinks; `-replay-hold` + `-dist` flags) (+ new `main_test.go`).
**UI wire (TS) — modified:** `ui/src/wire/contract.ts` → re-export adapter over `gen/wsmsg.ts` (+ new `contract.test.ts`).
**UI exec (TS) — modified:** `AccountBarPanel.tsx`, `OrderTicketPanel.tsx`, `PositionsPanel.tsx` (+ their existing `.test.tsx`).
**UI E2E (TS) — new:** `ui/playwright.config.ts`, `ui/e2e/serve.sh`, `ui/e2e/smoke.spec.ts`, `ui/e2e/error-matrix.spec.ts`.
**UI fixture tooling (TS) — new:** `ui/mock-engine/capture.ts`, `ui/fixtures/session-e2e.json`.
**UI polish (TS) — modified:** `ChartApiFacade.ts`, `ChartPanel.tsx`, `ChartController.ts` (+ `.test.ts`), `WorkspaceHeader.tsx` (+ existing `.test.tsx`), `ErrorBoundary.tsx` (+ existing `.test.tsx`), `ConnectionStatusPanel.tsx` (+ existing `src/chrome/ConnectionStatusPanel.test.tsx`).

---

### Task 1: Synthetic journal generator (`cmd/genjournal`)

**Files:**
- Create: `engine/cmd/genjournal/main.go`
- Test: `engine/cmd/genjournal/main_test.go`

**Interfaces:**
- Consumes: `store.Open(store.Options{Path, Clock}) (*store.Store, error)`, `(*store.Store).RecordEvent(feed.Event, recvMs int64)`, `(*store.Store).Close() error`, `(*store.Store).ReadJournalDay(day string) ([]store.JournalRow, error)`; `feed.QuoteEvent/BookEvent/TicksEvent/Bars1mEvent` wrapping `feed.Quote/Book/BookLevel/Tick/Bar`; `feed.Buy`/`feed.Sell` (`feed.Direction` consts, verified exported); `session.Loc()`; `clock.System{}`.
- Produces: a CLI `go run ./cmd/genjournal -db <path> -day YYYY-MM-DD` writing a deterministic day for `US.AAPL` + `US.NVDA`. Reused by Task 7's `serve.sh` and Task 10's capture.

**Background:** `RecordEvent(ev, recvMs)` partitions rows by the ET day of `recvMs` (`store/codec.go:dayKey`) and pulls the event's exchange ts from the event itself (`eventExchTs`). So constructing events with `TsMs`/`BucketMs` inside the target ET day, and passing that same ms as `recvMs`, makes `ReadJournalDay(day)` return them in seq order. `Close()` flushes the writer queue before returning. Emit **ticks** (→ 10s bars, tape, marks) and **1m bars** (→ 1m + local 5m/60m), plus a quote and a book, per symbol.

- [ ] **Step 1: Write the failing test**

Create `engine/cmd/genjournal/main_test.go`:

```go
package main

import (
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestGenerateWritesReplayableDay(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gen.db")
	const day = "2026-01-02"

	if err := generate(db, day); err != nil {
		t.Fatalf("generate: %v", err)
	}

	st, err := store.Open(store.Options{Path: db, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rows, err := st.ReadJournalDay(day)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows written")
	}

	kinds := map[string]map[string]int{"US.AAPL": {}, "US.NVDA": {}}
	for _, r := range rows {
		if m, ok := kinds[r.Symbol]; ok {
			m[r.Kind]++
		}
	}
	for sym, m := range kinds {
		for _, k := range []string{"ticks", "bars1m", "book", "quote"} {
			if m[k] == 0 {
				t.Errorf("%s: missing %q events (%v)", sym, k, m)
			}
		}
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("seq not increasing at %d", i)
		}
	}
	var sawTick bool
	for _, r := range rows {
		if te, ok := r.Event.(feed.TicksEvent); ok && len(te.Ticks) > 0 && te.Ticks[0].Price > 0 {
			sawTick = true
			break
		}
	}
	if !sawTick {
		t.Fatal("no decodable tick with a positive price")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./cmd/genjournal/`
Expected: FAIL — `undefined: generate`.

- [ ] **Step 3: Write the generator**

Create `engine/cmd/genjournal/main.go`:

```go
// Command genjournal writes a deterministic synthetic trading day into a SQLite
// journal, replayable by `etape -replay <day>`. It is the data source for the
// UI Plan 6 Playwright E2E and for re-capturing a mock-engine fixture — no OpenD,
// no market hours, byte-for-byte reproducible.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

var symbols = []struct {
	sym  string
	open float64
}{
	{"US.AAPL", 190.00},
	{"US.NVDA", 140.00},
}

const (
	bars    = 20 // 1m bars per symbol
	ticksPM = 6  // ticks per minute per symbol
)

func generate(dbPath, day string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	st, err := store.Open(store.Options{Path: dbPath, Clock: clock.System{}})
	if err != nil {
		return err
	}

	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		st.Close()
		return fmt.Errorf("bad -day %q: %w", day, err)
	}
	// Anchor at 09:30:00 ET so dayKey(recvMs) resolves to `day` and the bars
	// read as RTH for session shading.
	base := time.Date(d.Year(), d.Month(), d.Day(), 9, 30, 0, 0, session.Loc())

	for _, s := range symbols {
		px := s.open
		openMs := base.UnixMilli()
		st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{
			Symbol: s.sym, TsMs: openMs, Last: px, Open: px, High: px, Low: px,
			PrevClose: px, Volume: 0, Turnover: 0,
		}}, openMs)
		st.RecordEvent(feed.BookEvent{Book: bookAround(s.sym, openMs, px)}, openMs)

		for m := 0; m < bars; m++ {
			minStart := base.Add(time.Duration(m) * time.Minute)
			o := px
			hi, lo := px, px
			for k := 0; k < ticksPM; k++ {
				px += 0.05
				if k == ticksPM-1 {
					px -= 0.03
				}
				if px > hi {
					hi = px
				}
				if px < lo {
					lo = px
				}
				tickMs := minStart.Add(time.Duration(k) * 10 * time.Second).UnixMilli()
				dir := feed.Buy
				if k%2 == 1 {
					dir = feed.Sell
				}
				st.RecordEvent(feed.TicksEvent{Ticks: []feed.Tick{{
					Symbol: s.sym, Seq: int64(m*ticksPM + k), TsMs: tickMs,
					Price: round2(px), Volume: 100, Turnover: round2(px) * 100, Dir: dir,
				}}}, tickMs)
			}
			barMs := minStart.UnixMilli()
			st.RecordEvent(feed.Bars1mEvent{Bars: []feed.Bar{{
				Symbol: s.sym, BucketMs: barMs,
				O: round2(o), H: round2(hi), L: round2(lo), C: round2(px),
				Volume: int64(ticksPM * 100), Turnover: round2(px) * float64(ticksPM*100),
			}}}, barMs)
		}
	}

	return st.Close() // flushes the writer queue
}

func bookAround(sym string, tsMs int64, px float64) feed.Book {
	var bids, asks []feed.BookLevel
	for i := 1; i <= 5; i++ {
		bids = append(bids, feed.BookLevel{Price: round2(px - 0.01*float64(i)), Volume: int64(100 * i), Orders: int32(i)})
		asks = append(asks, feed.BookLevel{Price: round2(px + 0.01*float64(i)), Volume: int64(100 * i), Orders: int32(i)})
	}
	return feed.Book{Symbol: sym, TsMs: tsMs, Bids: bids, Asks: asks}
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func main() {
	db := flag.String("db", "", "output SQLite journal path (required)")
	day := flag.String("day", "2026-01-02", "ET trading day to stamp (YYYY-MM-DD)")
	flag.Parse()
	if *db == "" {
		log.Fatal("genjournal: -db is required")
	}
	if err := generate(*db, *day); err != nil {
		log.Fatalf("genjournal: %v", err)
	}
	log.Printf("genjournal: wrote %s for %s", *db, *day)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd engine && go test ./cmd/genjournal/`
Expected: PASS.

- [ ] **Step 5: Build + smoke-run**

Run: `cd engine && go build ./cmd/genjournal && go run ./cmd/genjournal -db /tmp/etape-e2e.db -day 2026-01-02`
Expected: logs `genjournal: wrote /tmp/etape-e2e.db for 2026-01-02`; file exists.

- [ ] **Step 6: Commit**

```bash
cd engine && gofmt -w ./cmd/genjournal && go vet ./cmd/genjournal
git add cmd/genjournal
git commit -m "feat(engine/genjournal): synthetic replayable-day generator for UI E2E"
```

---

### Task 2: Feed replayed marks to the sim broker

**Files:**
- Modify: `engine/cmd/etape/main.go` (`markBridge`, and the replay branch in `main`)
- Test: `engine/cmd/etape/main_test.go` (new)

**Interfaces:**
- Produces: `markBridge(ctx, core, execCore, sinks []markSink)` — forwards each mark to `exec.Core` **and** every sink; `markSink` interface `{ SetMark(string, float64) }`.

**Background (the gap):** `sim.Broker.SetMark` is called only from tests today. In a real `-replay` boot, `markBridge` feeds marks to `exec.Core` (for P&L/gate) but nothing feeds the sim broker, so a MARKET `SubmitOrder` hits `"sim: no mark for market order"` (`internal/broker/sim/sim.go:118`) and the E2E order never fills. Forward replayed marks into the sim brokers. (Verified: current `markBridge` is 3-arg at `main.go:303-313`; the call site `go markBridge(ctx, core, execCore)` is at `main.go:183`; `vbs []venueBroker` is in scope; `sim.Broker.SetMark(string, float64)` and `exec.Mark{Symbol,Price,TsMs}` match.)

- [ ] **Step 1: Write the failing test**

Create `engine/cmd/etape/main_test.go`:

```go
package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
)

type recordingSink struct {
	mu    sync.Mutex
	marks map[string]float64
}

func (r *recordingSink) SetMark(sym string, px float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.marks == nil {
		r.marks = map[string]float64{}
	}
	r.marks[sym] = px
}

func (r *recordingSink) get(sym string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.marks[sym]
	return v, ok
}

func TestMarkBridgeForwardsToSinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	core := md.New(md.Config{TapeRing: 1024, AnchorSecs: 9*3600 + 30*60})
	go func() { _ = core.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim-paper"}, Clock: clock.System{},
		Brokers: map[exec.VenueID]exec.Broker{}, IDGen: exec.NewOrderIDGen(clock.System{}, nil),
	})
	go func() { _ = execCore.Run(ctx) }()

	sink := &recordingSink{}
	go markBridge(ctx, core, execCore, []markSink{sink})

	core.Feed(feed.TicksEvent{Ticks: []feed.Tick{{
		Symbol: "US.AAPL", TsMs: time.Now().UnixMilli(), Price: 191.23, Volume: 100,
	}}})

	deadline := time.After(2 * time.Second)
	for {
		if v, ok := sink.get("US.AAPL"); ok {
			if v != 191.23 {
				t.Fatalf("mark = %v, want 191.23", v)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("sink never received a mark")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
```

(Verified: `exec.NewOrderIDGen(clock, nil)` is safe here — it only reads entropy on `.Next()`, which this test never calls. `md.Config{TapeRing, AnchorSecs}`, `exec.CoreConfig` fields, and a single-tick → `Marks()` emission are all confirmed.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./cmd/etape/ -run TestMarkBridgeForwardsToSinks`
Expected: FAIL — `markBridge` takes 3 args / `markSink` undefined.

- [ ] **Step 3: Add `markSink` + widen `markBridge`**

In `engine/cmd/etape/main.go`, replace the existing `markBridge` (lines 303-313) with:

```go
// markSink receives last-trade marks. Implemented by *sim.Broker (SetMark);
// used only in replay so submitted orders fill against the replayed tape.
type markSink interface{ SetMark(symbol string, price float64) }

// markBridge copies md.Core.Marks() -> exec.Core.FeedMark (the single md<->exec
// seam) and, in replay, -> every sim broker's SetMark so a submitted order fills
// against the replayed marks (live venues get marks from their own broker feed).
func markBridge(ctx context.Context, core *md.Core, execCore *exec.Core, sinks []markSink) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-core.Marks():
			execCore.FeedMark(exec.Mark{Symbol: m.Symbol, Price: m.Price, TsMs: m.TsMs})
			for _, s := range sinks {
				s.SetMark(m.Symbol, m.Price)
			}
		}
	}
}
```

- [ ] **Step 4: Collect sim sinks (replay only) and pass them in**

In `main`, just before the fan-in goroutines (the block around line 178-183), insert:

```go
	// In replay every venue is a SimBroker; forward replayed marks into them so
	// submitted orders fill. Live venues are fed by their own broker connection.
	var simSinks []markSink
	if !live {
		for _, vb := range vbs {
			if s, ok := vb.Broker.(markSink); ok {
				simSinks = append(simSinks, s)
			}
		}
	}
```

and change `go markBridge(ctx, core, execCore)` (line 183) to:

```go
	go markBridge(ctx, core, execCore, simSinks)
```

- [ ] **Step 5: Run the test + full build**

Run: `cd engine && go test ./cmd/etape/ -run TestMarkBridgeForwardsToSinks && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
cd engine && gofmt -w ./cmd/etape
git add cmd/etape/main.go cmd/etape/main_test.go
git commit -m "fix(engine/etape): feed replayed marks to sim brokers so replay orders fill"
```

---

### Task 3: Engine packaging flags — `-replay-hold` and `-dist`

**Files:**
- Modify: `engine/cmd/etape/main.go` (flag block; replay self-terminate; dist override)

**Interfaces:**
- Produces: `-dist <path>` (overrides `cfg.UIHub.DistDir`) and `-replay-hold` (in replay, don't self-terminate on journal exhaustion).

**Background:** the replay branch self-terminates via `go func() { pipeWG.Wait(); stop() }()` (main.go:215, verified byte-exact). With `-speed 0` the journal drains instantly, so without a hold the process exits before Playwright connects. `-replay-hold` keeps it up; the mirror retains full state, so a late subscriber gets a complete snapshot.

- [ ] **Step 1: Add the flags**

In `main`, in the flag block (the two new lines must sit between the `speed` flag at line 46 and `flag.Parse()` at line 47):

```go
	dist := flag.String("dist", "", "serve built UI from this dir (overrides [uihub].dist_dir)")
	replayHold := flag.Bool("replay-hold", false, "in replay, keep serving after the journal is exhausted (E2E)")
```

- [ ] **Step 2: Apply the `-dist` override**

Immediately after the `config.Load` error guard (after its closing `}` at line 56):

```go
	if *dist != "" {
		cfg.UIHub.DistDir = *dist
	}
```

- [ ] **Step 3: Honor `-replay-hold`**

Replace the self-terminate line (main.go:215):

```go
		go func() { pipeWG.Wait(); stop() }()         // self-terminate when the journal is exhausted
```

with:

```go
		if *replayHold {
			log.Info("replay-hold: serving last state until interrupted")
		} else {
			go func() { pipeWG.Wait(); stop() }() // self-terminate when the journal is exhausted
		}
```

- [ ] **Step 4: Build + integration smoke (the flags' verification)**

The flag wiring isn't unit-tested (that would require refactoring `main`); it's exercised by the Playwright harness (Task 7). Verify by hand here:

```bash
cd engine && go build ./cmd/etape
go run ./cmd/genjournal -db /tmp/etape-e2e.db -day 2026-01-02
cat > /tmp/etape-e2e.toml <<'EOF'
[store]
db_path = "/tmp/etape-e2e.db"
[uihub]
host = "127.0.0.1"
port = 8686
[[venue]]
id = "sim-paper"
broker = "sim"
env = "paper"
[gate.global]
max_day_loss = 100000
[gate.venue.sim-paper]
max_order_value = 100000
max_position_value = 100000
EOF
( cd ../ui && npm run build )   # produce ui/dist for the static route
go run ./cmd/etape -config /tmp/etape-e2e.toml -replay 2026-01-02 -speed 0 -replay-hold -dist "$(cd ../ui && pwd)/dist" &
ETAPE_PID=$!
sleep 2
curl -s http://127.0.0.1:8686/ | grep -q 'id="root"' && echo "STATIC OK" || echo "STATIC FAIL"
kill $ETAPE_PID
```

Expected: `STATIC OK` **and** the process still alive at `sleep 2` (proving `-replay-hold`).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w ./cmd/etape
git add cmd/etape/main.go
git commit -m "feat(engine/etape): -dist and -replay-hold flags for UI packaging + E2E"
```

---

### Task 4: Swap the UI onto the tygo-generated contract

**Files:**
- Modify: `ui/src/wire/contract.ts`
- Test: `ui/src/wire/contract.test.ts` (new)

**Interfaces:**
- Produces: `ui/src/wire/contract.ts` re-exporting everything from `gen/wsmsg.ts`, plus the **three** UI-only names the import sites rely on: `TopicName` (alias of `Topic`), `VenueID`, and `ScannerSession`.

**Background:** `gen/wsmsg.ts` is generated + drift-gated and is field-for-field identical to the hand-authored payloads/envelopes. The names in `contract.ts` **not** in `gen` are exactly three (verified by diffing all 60+ import sites): `TopicName` (gen calls it `Topic`), `VenueID` (gen uses bare `string` for `venue`; `VenueID` is imported as a type by `OrderTicketPanel.tsx`, `useOrderConfig.tsx`, `commands.ts`, `resolveTemplate.ts`, `actionTemplate.ts`), and `ScannerSession`. Converting `contract.ts` to a re-export makes the UI's types transitively come from `gen`, so an engine-side contract change breaks `npm run typecheck`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/wire/contract.test.ts`:

```ts
import { describe, it, expectTypeOf } from "vitest";
import type { Order, Quote, Bar, ExecStatus, SubmitOrderArgs, TopicName, VenueID, ScannerSession } from "./contract";
import type * as Gen from "../gen/wsmsg";

describe("contract re-exports the generated wire types", () => {
  it("payload types are the generated types", () => {
    expectTypeOf<Order>().toEqualTypeOf<Gen.Order>();
    expectTypeOf<Quote>().toEqualTypeOf<Gen.Quote>();
    expectTypeOf<Bar>().toEqualTypeOf<Gen.Bar>();
    expectTypeOf<ExecStatus>().toEqualTypeOf<Gen.ExecStatus>();
    expectTypeOf<SubmitOrderArgs>().toEqualTypeOf<Gen.SubmitOrderArgs>();
  });
  it("TopicName aliases the generated Topic union", () => {
    expectTypeOf<TopicName>().toEqualTypeOf<Gen.Topic>();
  });
  it("VenueID + ScannerSession stay UI-side string types", () => {
    expectTypeOf<VenueID>().toEqualTypeOf<string>();
    expectTypeOf<ScannerSession>().toEqualTypeOf<"premarket" | "rth" | "afterhours">();
  });
});
```

(Verified: `vitest@^1.6.0` provides `expectTypeOf`.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/wire/contract.test.ts`
Expected: FAIL — `TopicName` isn't yet an alias of `Gen.Topic`.

- [ ] **Step 3: Rewrite `contract.ts` as the adapter**

Replace the entire contents of `ui/src/wire/contract.ts` with:

```ts
// WIRE CONTRACT ADAPTER. The wire types are generated by tygo from the engine's
// uihub/wsmsg package into ui/src/gen/wsmsg.ts (source of truth; drift-gated by
// `make gen-ts-check` in engine/). This module re-exports them under the names
// the UI imports, adding only the three UI-side conventions the generator does
// not emit: TopicName (alias of the generated `Topic`), VenueID, and
// ScannerSession.
//
// md.indicator keying: single-series indicators (VWAP/EMA/SMA/VOLUME/DELTA)
// stream under the bare instanceId as the message `key`; MACD streams each slot
// under `${instanceId}#${slot}` (macd/signal/hist). See render/chart/
// indicatorSeries.ts's describeIndicator for slot names.
export * from "../gen/wsmsg";
import type { Topic } from "../gen/wsmsg";

// The UI has always called the topic union `TopicName`; keep that name.
export type TopicName = Topic;

// A venue id is a free-form config slug; the generated types use bare `string`
// for `venue`, but the UI names the concept for readability.
export type VenueID = string;

// Scanner session travels on the message `key` ("premarket" | "rth" |
// "afterhours"). A UI-side convention, not a generated wire type.
export type ScannerSession = "premarket" | "rth" | "afterhours";
```

- [ ] **Step 4: Run the type test + full typecheck + whole UI suite**

Run: `cd ui && npx vitest run src/wire/contract.test.ts && npm run typecheck && npm run test`
Expected: all PASS. The full typecheck is the real proof the 60+ consumers still resolve.

- [ ] **Step 5: Confirm the drift gate is still green**

Run: `cd engine && make gen-ts-check`
Expected: PASS (this task does not touch `gen/wsmsg.ts`).

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/wire/contract.ts src/wire/contract.test.ts
git commit -m "refactor(ui/wire): consume tygo-generated gen/wsmsg via contract adapter"
```

---

### Task 5: Per-venue arm control (safety-critical) — AccountBar

**Files:**
- Modify: `ui/src/chrome/panels/AccountBarPanel.tsx`
- Test: `ui/src/chrome/panels/AccountBarPanel.test.tsx`

**Interfaces:**
- Consumes: `oc.arm(venue)` / `oc.disarm(venue)` (`OrderCommands`, verified send `Arm`/`Disarm` with `{venue}`), `stores.exec.status().venues[i].venueArmed`.
- Produces: clickable per-venue arm toggles with `data-testid={`venue-arm-${venue}`}` and `data-armed={venueArmed}` (consumed by Task 8's E2E).

**Background (the gap):** the two-layer gate needs both master and the venue armed (`gate.go:72-78`), and `useHotkeys.ts:30` already blocks on `venueArmed` — but the UI can only arm **master** (`arm-toggle` → `oc.arm()` with no venue); the per-venue dots (`AccountBarPanel.tsx:43-47`) are display-only. Against the real engine every order is blocked "venue disarmed" (the mock-engine masked this by treating one bool as both). Make the per-venue indicators interactive.

- [ ] **Step 1: Write the failing test**

Add a test to `ui/src/chrome/panels/AccountBarPanel.test.tsx`. **Reuse the file's existing setup exactly** — it has `// @vitest-environment jsdom` (line 1), imports `makeStores` from `../../data/registry` and `render/screen/act/fireEvent` from `@testing-library/react`, and seeds exec status via `stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(...) })` with a local `status(masterArmed)` factory (there is **no** `applyStatus` method). Mirror that:

```tsx
it("arms a venue when its per-venue control is clicked", async () => {
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = {
    sendCommand: async (name: string, args: unknown) => { sent.push({ name, args }); return { kind: "ack", corrId: "1", status: "accepted" } as const; },
    sendQuery: async () => ({ kind: "result", corrId: "1", payload: [] } as const),
  };
  const stores = makeStores();
  // Reuse the file's `status()` factory shape; venue disarmed here.
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: {
    masterArmed: false, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues: [{ venue: "sim-paper", broker: "alpaca", connected: true, venueArmed: false, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
  }});

  render(<AccountBarPanel stores={stores} commands={commands} /* ...remaining PanelProps as the file's other tests pass them */ />);
  const btn = await screen.findByTestId("venue-arm-sim-paper");
  expect(btn).toHaveAttribute("data-armed", "false");
  fireEvent.click(btn);
  expect(sent).toContainEqual({ name: "Arm", args: { venue: "sim-paper" } });
});
```

Match how the file's existing tests construct `PanelProps` (theme/toast come from `AccountBarPanel`'s own `useTheme()`/`useToasts()` — check whether the file wraps in providers or those hooks tolerate defaults; reuse its pattern verbatim).

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/AccountBarPanel.test.tsx`
Expected: FAIL — no `venue-arm-sim-paper` element.

- [ ] **Step 3: Make the per-venue indicator a control**

In `ui/src/chrome/panels/AccountBarPanel.tsx`, replace the per-venue `<span>` (lines 43-47) with a button that toggles that venue's arm:

```tsx
        {(status?.venues ?? []).map((v) => (
          <button key={v.venue} data-testid={`venue-arm-${v.venue}`} data-armed={v.venueArmed}
            title={`${v.venue}: ${v.connected ? "connected" : "disconnected"} — click to ${v.venueArmed ? "disarm" : "arm"}`}
            onClick={() => (v.venueArmed ? oc.disarm(v.venue) : oc.arm(v.venue))}
            style={{ display: "flex", gap: 3, alignItems: "center", fontSize: 10, cursor: "pointer",
              background: "transparent", border: `1px solid ${v.venueArmed ? palette.up : palette.border}`,
              borderRadius: 4, padding: "2px 6px", color: v.venueArmed ? palette.up : palette.textMuted }}>
            {dot(v.connected, `${v.venue}: ${v.connected ? "connected" : "disconnected"}`)}{v.venue}{v.venueArmed ? " ●" : " ○"}
          </button>
        ))}
```

- [ ] **Step 4: Run the test + typecheck**

Run: `cd ui && npx vitest run src/chrome/panels/AccountBarPanel.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/panels/AccountBarPanel.tsx src/chrome/panels/AccountBarPanel.test.tsx
git commit -m "feat(ui/exec): per-venue arm control in the account bar (two-layer gate)"
```

---

### Task 6: Armed-state indicator on the order ticket + Flatten (+ E2E testid)

**Files:**
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx`, `ui/src/chrome/panels/PositionsPanel.tsx`
- Test: `ui/src/chrome/panels/OrderTicketPanel.test.tsx`, `ui/src/chrome/panels/PositionsPanel.test.tsx`

**Interfaces:**
- Consumes: `stores.exec.status()?.masterArmed` and `status.venues.find(v => v.venue === venue)?.venueArmed` (same accessor `AccountBarPanel` uses). `OrderTicketPanel` already computes `venue` (line 41: `orderCfg.activeVenue || venues[0] || ""`).
- Produces: an armed/disarmed badge on the ticket (`data-testid="ticket-armed-state"`), an armed annotation on Flatten, **and** a `data-testid="order-type"` on the ticket's type `<select>` (needed by Task 8's MARKET submit).

**Background:** Plan 5 Important finding — the ticket Submit/presets and Positions Flatten have no client-side armed indicator. `OrderTicketPanel` already fetches `status` (line 39). The ticket's type `<select>` (line 105) has no testid, which Task 8 needs to switch to MARKET — add it here since we're already editing this file.

- [ ] **Step 1: Write the ticket test**

Add to `ui/src/chrome/panels/OrderTicketPanel.test.tsx`. **Reuse the file's existing `wrap()` helper** (it wraps `<ThemeProvider><ToastProvider><OrderConfigProvider commands={...}>…` — `OrderTicketPanel` calls `useOrderConfig()`, which throws without the provider) and its `status()` factory + `stores.exec.apply({ kind:"snapshot", topic:"exec.status" as never, payload })` seeding:

```tsx
it("shows a DISARMED badge when master or the active venue is disarmed", async () => {
  const stores = makeStores();
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: {
    masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues: [{ venue: "sim-paper", broker: "alpaca", connected: true, venueArmed: false, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
  }});
  render(wrap({ stores /* + the other PanelProps the helper needs */ }));
  expect(await screen.findByTestId("ticket-armed-state")).toHaveTextContent(/DISARMED/i);
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: FAIL — no `ticket-armed-state` element.

- [ ] **Step 3: Add the badge + the order-type testid**

In `OrderTicketPanel.tsx`, after the `venue` computation (line 41) add:

```tsx
  const vStatus = status?.venues.find((v) => v.venue === venue);
  const armed = (status?.masterArmed ?? false) && (vStatus?.venueArmed ?? false);
```

Add `data-testid="order-type"` to the type select (line 105):

```tsx
        <select data-testid="order-type" value={type} onChange={(e) => setType(e.target.value as OrderType)} style={inp}>{TYPES.map((t) => <option key={t}>{t}</option>)}</select>
```

Insert the badge immediately before the Submit `<button>` (line 114):

```tsx
      <div data-testid="ticket-armed-state" style={{ fontSize: 11, fontWeight: 700, textAlign: "center", padding: "2px 0",
        color: armed ? palette.up : palette.warn }}>
        {armed ? "ARMED" : "DISARMED — order will be blocked"}
      </div>
```

Do not disable Submit (the engine gate is still the authority; this is an indicator).

- [ ] **Step 4: Positions Flatten armed annotation**

Write a failing test in `PositionsPanel.test.tsx` (reuse its status-seeding pattern; assert a `data-armed` attribute on the Flatten button), then in `PositionsPanel.tsx` add near the top:

```tsx
  const status = stores.exec.status();
  const armedFor = (v: string | null) => !!status?.masterArmed && !!status?.venues.find((x) => x.venue === v)?.venueArmed;
```

and annotate each Flatten button (lines 52-55), keeping it clickable (flatten is exposure-reducing):

```tsx
                  <button data-testid={`flatten-${r.venue}-${r.symbol}`} data-armed={armedFor(r.venue)}
                    title={armedFor(r.venue) ? "Flatten position" : "Venue disarmed — flatten still allowed (exposure-reducing)"}
                    onClick={() => flatten(r)} style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Flatten</button>
```

- [ ] **Step 5: Run both tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx src/chrome/panels/PositionsPanel.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/OrderTicketPanel.tsx src/chrome/panels/OrderTicketPanel.test.tsx src/chrome/panels/PositionsPanel.tsx src/chrome/panels/PositionsPanel.test.tsx
git commit -m "feat(ui/exec): armed-state indicator on order ticket + Flatten; order-type testid"
```

---

### Task 7: Playwright harness (install + config + webServer)

**Files:**
- Create: `ui/playwright.config.ts`, `ui/e2e/serve.sh`
- Modify: `ui/package.json` (devDep + `e2e` scripts), `ui/.gitignore`

**Interfaces:**
- Produces: `npm run e2e` — builds the UI, generates the synthetic journal, boots the real engine (`-replay 2026-01-02 -speed 0 -replay-hold -dist <ui/dist>`), and runs the specs against `http://127.0.0.1:8686`. `webServer` handles start/stop/wait.

**Background:** Playwright is not installed today (verified). The engine serving `ui/dist` at `/` means the E2E hits the **production bundle**. `webServer.url` polling makes the harness race-free.

- [ ] **Step 1: Install Playwright + Chromium**

Run: `cd ui && npm i -D @playwright/test@^1.48.0 && npx playwright install chromium`
Expected: `@playwright/test` in devDependencies; Chromium downloaded.

- [ ] **Step 2: Add the serve script**

Create `ui/e2e/serve.sh`:

```bash
#!/usr/bin/env bash
# Boots the real engine for the Playwright E2E: builds the UI, generates a
# synthetic replay day, writes a config pointing at it + ui/dist, then execs
# the engine in replay-hold mode. exec so Playwright's teardown reaches it.
set -euo pipefail

UI_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$UI_DIR/.." && pwd)"
ENGINE_DIR="$ROOT/engine"
DAY="2026-01-02"
WORK="$(mktemp -d)"
DB="$WORK/e2e.db"
CFG="$WORK/e2e.toml"

echo "e2e: building UI bundle" >&2
(cd "$UI_DIR" && npm run build >&2)

echo "e2e: generating synthetic journal ($DB)" >&2
(cd "$ENGINE_DIR" && go run ./cmd/genjournal -db "$DB" -day "$DAY" >&2)

cat > "$CFG" <<EOF
[store]
db_path = "$DB"
[uihub]
host = "127.0.0.1"
port = 8686
[[venue]]
id = "sim-paper"
broker = "sim"
env = "paper"
[gate.global]
max_day_loss = 100000
max_symbol_position_value = 100000
max_symbol_position_shares = 100000
[gate.venue.sim-paper]
max_order_value = 100000
max_position_value = 100000
max_position_shares = 100000
max_open_orders = 50
EOF

echo "e2e: booting engine (replay $DAY, hold)" >&2
cd "$ENGINE_DIR"
exec go run ./cmd/etape -config "$CFG" -replay "$DAY" -speed 0 -replay-hold -dist "$UI_DIR/dist"
```

- [ ] **Step 3: Add the Playwright config**

Create `ui/playwright.config.ts`:

```ts
import { defineConfig, devices } from "@playwright/test";

// The E2E boots the REAL engine (replay mode) serving the production ui/dist
// bundle. No CI — this is a local run on Earl's mac (`npm run e2e`).
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // one shared engine + one WS-backed origin
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "e2e/.report" }]],
  use: {
    baseURL: "http://127.0.0.1:8686",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash e2e/serve.sh",
    url: "http://127.0.0.1:8686/",
    reuseExistingServer: false,
    timeout: 120_000, // includes the UI build + go run compile on first boot
    stdout: "pipe",
    stderr: "pipe",
  },
});
```

- [ ] **Step 4: Wire package.json scripts + gitignore**

In `ui/package.json` `scripts`, add `"e2e": "playwright test"` and `"e2e:ui": "playwright test --ui"`. In `ui/.gitignore`, append:

```
# Playwright
/e2e/.report
/e2e/.artifacts
/test-results
/playwright-report
```

- [ ] **Step 5: Prove the harness boots with a minimal spec**

`chmod +x ui/e2e/serve.sh`, then create `ui/e2e/smoke.spec.ts` (fleshed out in Task 8):

```ts
import { test, expect } from "@playwright/test";

test("engine serves the production bundle and the account bar hydrates", async ({ page }) => {
  await page.goto("/?workspace=trading");
  await expect(page.locator("#root")).toBeVisible();
  await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
});
```

- [ ] **Step 6: Run it**

Run: `cd ui && npm run e2e`
Expected: PASS — Playwright builds the UI, boots the engine, loads the served bundle, finds a live account bar. `acct-equity` will read `$0.00` (sim account seeds zero equity) — visible is what matters. If it never appears, check the piped engine stderr for a boot error.

- [ ] **Step 7: Commit**

```bash
cd ui && git add playwright.config.ts e2e/serve.sh e2e/smoke.spec.ts package.json package-lock.json .gitignore
git commit -m "test(ui/e2e): Playwright harness booting the real engine on ui/dist (replay)"
```

---

### Task 8: Smoke E2E — both workspaces, data, link focus, order lifecycle

**Files:**
- Modify: `ui/e2e/smoke.spec.ts`

**Interfaces:**
- Consumes: the harness; testids `acct-equity`, `arm-toggle`, `venue-arm-sim-paper`/`data-armed` (Task 5), `submit`, `order-type` (Task 6); the WorkspaceHeader per-group inputs `aria-label="focus green"` (verified: four identical `placeholder="symbol"` inputs, one per link group; no `<header>` element). The trading `order-ticket` is in the **green** link group and renders `<strong>{bareSymbol(symbol)}</strong>` (defaults to `AAPL`), so it follows green-group focus.

**Background:** chart/ladder/tape are canvas, so assert **DOM proxies** for data flow + screenshot artifacts. The order **fills** only because Task 2 fed marks to the sim broker and Task 5 lets the UI arm the venue — a FILLED order transitively proves md→marks→exec→fill. `OpenOrdersPanel` renders status via `STATUS_LABEL.FILLED = "Filled"` (case-insensitive `getByText` matches). The ticket must be switched to **MARKET** (LIMIT+empty-price is blocked client-side).

- [ ] **Step 1: Trading workspace mounts + account hydrates**

Replace `smoke.spec.ts` with:

```ts
import { test, expect, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";

const ART = "e2e/.artifacts";
mkdirSync(ART, { recursive: true });
const shot = (page: Page, name: string) => page.screenshot({ path: `${ART}/${name}.png`, fullPage: true });

test.describe("trading workspace", () => {
  test("panels mount and the account bar hydrates", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId("submit")).toBeVisible(); // order ticket mounted
    await shot(page, "trading-loaded"); // eyeball: charts populated + ladder painted (canvas)
  });
});
```

- [ ] **Step 2: Run it**

Run: `cd ui && npm run e2e -- smoke.spec.ts`
Expected: PASS; `e2e/.artifacts/trading-loaded.png` shows populated charts + a painted ladder (eyeball once — this is the real-browser confirmation Plan 5 gap (c) asked for). If charts look cold, confirm the trading charts key on `US.AAPL` and the journal covers it.

- [ ] **Step 3: Order lifecycle (MARKET) → FILLED + fill diamond**

Append inside the trading describe:

```ts
  test("a paper MARKET order walks to Filled and paints a fill diamond", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    // Two-layer gate: arm master, then the venue.
    await page.getByTestId("arm-toggle").click();
    await expect(page.getByTestId("arm-toggle")).toHaveText("ARMED");
    await page.getByTestId("venue-arm-sim-paper").click();
    await expect(page.getByTestId("venue-arm-sim-paper")).toHaveAttribute("data-armed", "true");

    // MARKET order needs no price; default 100 shares of AAPL fills at the mark.
    await page.getByTestId("order-type").selectOption("MARKET");
    await page.getByTestId("submit").click();

    await expect(page.getByText("Filled").first()).toBeVisible({ timeout: 10_000 }); // OpenOrders status
    await shot(page, "trading-filled"); // eyeball: fill diamond on the chart
  });
```

- [ ] **Step 4: Run it**

Run: `cd ui && npm run e2e -- smoke.spec.ts`
Expected: PASS. `trading-filled.png` shows the fill diamond (eyeball). If the order shows a block toast: "venue disarmed" ⇒ Task 5 missing; "no mark" ⇒ Task 2 missing.

- [ ] **Step 5: Monitoring workspace + link focus**

Append:

```ts
test.describe("monitoring workspace", () => {
  test("loads; scanner/news show their empty state (no pollers in replay)", async ({ page }) => {
    await page.goto("/?workspace=monitoring");
    // Charts are canvas; assert a deterministic empty-state text + screenshot.
    // Confirm the exact copy in ScannerPanel.tsx / NewsPanel.tsx and tighten this regex.
    await expect(page.getByText(/no symbols match|no symbol focused/i).first()).toBeVisible({ timeout: 15_000 });
    await page.screenshot({ path: "e2e/.artifacts/monitoring-loaded.png", fullPage: true });
  });
});

test.describe("link groups", () => {
  test("focusing the green group moves its panels together", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    // Four identical symbol inputs exist (one per group); target green by aria-label.
    const green = page.getByLabel("focus green");
    await green.fill("NVDA");
    await green.press("Enter");
    // The order ticket is in the green group; its symbol label follows the focus.
    await expect(page.getByText("NVDA", { exact: false }).first()).toBeVisible({ timeout: 10_000 });
    await page.screenshot({ path: "e2e/.artifacts/link-focus-nvda.png", fullPage: true });
  });
});
```

- [ ] **Step 6: Run the full smoke + commit**

Run: `cd ui && npm run e2e -- smoke.spec.ts`
Expected: all PASS; artifact PNGs written. (If the monitoring empty-state regex misses, open the screenshot and copy the panel's actual empty text into the matcher.)

```bash
cd ui && git add e2e/smoke.spec.ts
git commit -m "test(ui/e2e): smoke — workspaces, link focus, MARKET order lifecycle, fill diamond"
```

---

### Task 9: Error-matrix E2E

**Files:**
- Create: `ui/e2e/error-matrix.spec.ts`

**Interfaces:**
- Consumes: the harness; `ReconnectOverlay` (renders `"connecting…"` / `"reconnecting…"`, verified); the order-reject block toast (`OrderCommands.submit` pushes `` `Blocked: ${reason}` ``).

**Background:** automate the reliably-triggerable modes. WS disconnect via `context.setOffline`; engine-down-at-load via `page.route("**/ws", abort)`; order-reject via a disarmed **MARKET** submit (LIMIT+empty-price would be blocked client-side before reaching the gate, so it must be MARKET to exercise the real gate block). **StreamGap** can't be triggered deterministically from the browser — note it, don't fake it.

- [ ] **Step 1: WS drop → reconnect overlay shows then clears**

Create `ui/e2e/error-matrix.spec.ts`:

```ts
import { test, expect } from "@playwright/test";

test.describe("error-handling matrix", () => {
  test("WS drop shows the reconnect overlay, recovery clears it", async ({ page, context }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });

    await context.setOffline(true);
    await expect(page.getByText(/reconnect|disconnected|connecting/i).first()).toBeVisible({ timeout: 10_000 });

    await context.setOffline(false);
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
  });
});
```

Confirm the overlay copy in `ui/src/chrome/ReconnectOverlay.tsx` and tighten the regex.

- [ ] **Step 2: Engine unreachable at load → connecting/error state**

Append:

```ts
  test("engine unreachable at load stays in a connecting/error state", async ({ page }) => {
    await page.route("**/ws", (route) => route.abort());
    await page.goto("/?workspace=trading");
    await expect(page.getByText(/reconnect|connecting|disconnected/i).first()).toBeVisible({ timeout: 15_000 });
  });
```

- [ ] **Step 3: Disarmed MARKET submit → verbatim gate block toast**

Append:

```ts
  test("submitting a MARKET order while disarmed surfaces the gate block", async ({ page }) => {
    await page.goto("/?workspace=trading");
    await expect(page.getByTestId("acct-equity")).toBeVisible({ timeout: 15_000 });
    // Do NOT arm. MARKET so it clears client pre-checks and reaches the engine gate.
    await page.getByTestId("order-type").selectOption("MARKET");
    await page.getByTestId("submit").click();
    await expect(page.getByText(/blocked|disarm|master/i).first()).toBeVisible({ timeout: 10_000 });
  });
```

- [ ] **Step 4: Document the StreamGap non-automation**

Append:

```ts
// NOTE: StreamGap (outbound-queue overflow → forced re-snapshot → the StreamGap
// badge) is not deterministically triggerable from the browser and is not
// automated here. It retains unit coverage (WsClient re-snapshot on gap; the
// OpenOrders StreamGap badge render). Verify manually if the badge changes.
```

- [ ] **Step 5: Run + commit**

Run: `cd ui && npm run e2e -- error-matrix.spec.ts`
Expected: PASS (adjust the overlay/toast regexes to the real copy if a matcher misses).

```bash
cd ui && git add e2e/error-matrix.spec.ts
git commit -m "test(ui/e2e): error matrix — reconnect overlay, engine-down, disarmed gate block"
```

---

### Task 10: Re-capture a combined fixture from the real engine

**Files:**
- Create: `ui/mock-engine/capture.ts`, `ui/fixtures/session-e2e.json`
- Modify: `ui/package.json` (`capture` script), `ui/mock-engine/run.ts` (catalog comment)

**Interfaces:**
- Consumes: a running engine (Task 7 harness or a manual replay boot) on `ws://127.0.0.1:8686/ws`; the `Fixture` shape from `ui/mock-engine/server.ts` (`{ snapshots, deltas }`, verified).
- Produces: `capture.ts` (connect → subscribe all topics → record frames → write a fixture JSON) and a new combined `session-e2e.json` (candles + book + tape) for dev + a combined candle+exec dev flow.

**Background:** existing fixtures are hand-written but already shape-correct (zero tygo drift), so the high-value output is a **captured combined fixture**, not a wholesale overwrite (which would churn component tests that assert fixed values). `ws`/`@types/ws` are already devDeps.

- [ ] **Step 1: Write the capture tool**

Create `ui/mock-engine/capture.ts`:

```ts
// Captures live engine frames into the mock-engine Fixture format. Point it at a
// running engine (e.g. `etape -replay 2026-01-02 -speed 0 -replay-hold`):
//   tsx mock-engine/capture.ts session-e2e
// It subscribes to every topic in the wire contract, records the first snapshot
// per (topic,key) and a bounded set of subsequent deltas, then writes
// ui/fixtures/<name>.json.
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import WebSocket from "ws";

const here = dirname(fileURLToPath(import.meta.url));
const name = process.argv[2] ?? "session-e2e";
const url = process.env.ENGINE_WS ?? "ws://127.0.0.1:8686/ws";

// Full Topic union from gen/wsmsg.ts (16 topics).
const TOPICS = [
  "md.quote", "md.book", "md.tape", "md.bars", "md.indicator",
  "scanner.rank", "scanner.hit", "news.item",
  "exec.account", "exec.positions", "exec.orders", "exec.fills", "exec.status",
  "sys.health", "sys.events", "config",
];

const snapshots: Array<{ topic: string; key?: string; payload: unknown }> = [];
const deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }> = [];
const seenSnap = new Set<string>();
const t0 = Date.now();

const ws = new WebSocket(url);
ws.on("open", () => {
  for (const topic of TOPICS) ws.send(JSON.stringify({ kind: "subscribe", topic }));
  setTimeout(finish, 3000); // capture window
});
ws.on("message", (raw) => {
  const m = JSON.parse(raw.toString()) as { kind: string; topic?: string; key?: string; payload?: unknown };
  if (!m.topic) return;
  const id = `${m.topic}#${m.key ?? ""}`;
  if (m.kind === "snapshot" && !seenSnap.has(id)) {
    seenSnap.add(id);
    snapshots.push({ topic: m.topic, ...(m.key ? { key: m.key } : {}), payload: m.payload });
  } else if (m.kind === "delta" && deltas.length < 200) {
    deltas.push({ afterMs: Math.max(0, Date.now() - t0), topic: m.topic, ...(m.key ? { key: m.key } : {}), payload: m.payload });
  }
});
ws.on("error", (e) => { console.error("capture: ws error", e); process.exit(1); });

function finish() {
  const path = join(here, "..", "fixtures", `${name}.json`);
  writeFileSync(path, JSON.stringify({ snapshots, deltas }, null, 2) + "\n");
  console.log(`capture: wrote ${path} (${snapshots.length} snapshots, ${deltas.length} deltas)`);
  ws.close();
  process.exit(0);
}
```

- [ ] **Step 2: Add the script**

In `ui/package.json` `scripts`: `"capture": "tsx mock-engine/capture.ts"`.

- [ ] **Step 3: Capture against a live replay boot**

```bash
cd engine && go run ./cmd/genjournal -db /tmp/etape-cap.db -day 2026-01-02
# reuse /tmp/etape-e2e.toml from Task 3 (db_path=/tmp/etape-cap.db, sim-paper venue)
go run ./cmd/etape -config /tmp/etape-e2e.toml -replay 2026-01-02 -speed 0 -replay-hold &
cd ../ui && npm run capture -- session-e2e
kill %1 2>/dev/null || true
```

Expected: `ui/fixtures/session-e2e.json` with `md.bars`/`md.book`/`md.tape`/`md.quote` snapshots for `US.AAPL`/`US.NVDA`. (Exec topics are sparse until an order is submitted — fine; the fixture's job is the candle+book+tape combination.)

- [ ] **Step 4: Catalog + validate**

In `ui/mock-engine/run.ts`, add to the fixture-selection comment (near line 16):

```ts
//   npm run mock-engine -- session-e2e       (combined candles + book + tape, captured from replay)
```

Spot-check a captured topic's field names against the corresponding hand-written fixture (`chart-session.json` `md.bars`, `ladder-tape.json` `md.book`) to confirm no drift. Do **not** overwrite those files. Record the outcome in the commit message.

- [ ] **Step 5: Commit**

```bash
cd ui && git add mock-engine/capture.ts fixtures/session-e2e.json mock-engine/run.ts package.json
git commit -m "test(ui/fixtures): engine frame-capture tool + combined session-e2e fixture"
```

---

### Task 11: Remove dead `ChartApiFacade.isAtRightEdge`

**Files:**
- Modify: `ui/src/render/chart/ChartApiFacade.ts`, `ui/src/chrome/panels/ChartPanel.tsx`, `ui/src/render/chart/ChartController.test.ts`

**Background:** `isAtRightEdge()` is declared (`ChartApiFacade.ts:20`), implemented (`ChartPanel.tsx:40-43`), and only referenced by the test's fake — never invoked by `ChartController` (auto-follow is LWC's job). The test fake also declares a `setRightEdge` setter in its facade **type** (`ChartController.test.ts:15`), assigns it (line 17), and **calls it** (line 82, inside "does not force-scroll when the user has scrolled back") — all vestigial. Remove the whole cluster or the file won't compile.

- [ ] **Step 1: Remove the interface member**

In `ChartApiFacade.ts`, delete line 20 (`isAtRightEdge(): boolean;`).

- [ ] **Step 2: Remove the implementation**

In `ChartPanel.tsx`, delete the `isAtRightEdge` property (lines 40-43).

- [ ] **Step 3: Remove all test-fake traces**

In `ChartController.test.ts`, delete: the `atRightEdge` variable (line 14), the `setRightEdge` entry in the facade's **type** (line 15), the `setRightEdge` assignment (line 17), the `isAtRightEdge` fake property (line 25), **and** the `facade.setRightEdge(false)` call at line 82. If removing line 82 empties the "scrolled back" test's premise, keep the test but drop only that call (it never affected the assertions, since `ChartController` never reads `isAtRightEdge`).

- [ ] **Step 4: Verify nothing else references it + build**

Run: `cd ui && grep -rn "isAtRightEdge\|setRightEdge" src ; npm run typecheck && npx vitest run src/render/chart/ChartController.test.ts`
Expected: grep returns nothing; typecheck + tests PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/render/chart/ChartApiFacade.ts src/chrome/panels/ChartPanel.tsx src/render/chart/ChartController.test.ts
git commit -m "refactor(ui/chart): drop dead ChartApiFacade.isAtRightEdge + setRightEdge test fake"
```

---

### Task 12: `normalizeSymbol` case hardening

**Files:**
- Modify: `ui/src/chrome/WorkspaceHeader.tsx`
- Test: `ui/src/chrome/WorkspaceHeader.test.tsx` (**already exists** — extend it, do not create a new file)

**Background:** `normalizeSymbol` (`WorkspaceHeader.tsx:17-20`) returns the original-case string on the already-qualified branch (`us.nvda` → `us.nvda`) and concatenates the raw ticker on the other (`aapl` → `US.aapl`). Both should uppercase. `WorkspaceHeader.test.tsx` already has a `describe("normalizeSymbol")` block (only uppercase-input cases today, so the bug isn't exercised). The live UI upper-cases input on change, so the bug is only reachable by calling `normalizeSymbol` directly — which the new tests do.

- [ ] **Step 1: Add failing cases to the existing describe block**

In `ui/src/chrome/WorkspaceHeader.test.tsx`, add inside the existing `describe("normalizeSymbol", …)`:

```ts
  it("uppercases a bare lowercase ticker", () => {
    expect(normalizeSymbol("aapl")).toBe("US.AAPL");
  });
  it("uppercases an already-qualified lowercase symbol", () => {
    expect(normalizeSymbol("us.nvda")).toBe("US.NVDA");
  });
  it("uppercases a lowercase dotted US ticker", () => {
    expect(normalizeSymbol("brk.b")).toBe("US.BRK.B");
  });
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/WorkspaceHeader.test.tsx`
Expected: FAIL on the new lowercase cases.

- [ ] **Step 3: Fix both branches**

In `WorkspaceHeader.tsx`, change the return (line 19) to operate on the uppercased copy:

```ts
export function normalizeSymbol(raw: string): string {
  const upper = raw.toUpperCase();
  return MARKET_PREFIXES.some((p) => upper.startsWith(p)) ? upper : `US.${upper}`;
}
```

- [ ] **Step 4: Run the test + typecheck**

Run: `cd ui && npx vitest run src/chrome/WorkspaceHeader.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/WorkspaceHeader.tsx src/chrome/WorkspaceHeader.test.tsx
git commit -m "fix(ui/chrome): normalizeSymbol uppercases both branches"
```

---

### Task 13: Dedup `SubscribeIndicator` payload in ChartController

**Files:**
- Modify: `ui/src/render/chart/ChartController.ts`
- Test: `ui/src/render/chart/ChartController.test.ts`

**Background:** `addIndicator` (lines 115-118) and `resetForReload` (lines 154-157) build the identical `{ instanceId, symbol, timeframe, type, params }` `SubscribeIndicator` payload. The existing `commandSpy()` fake (test lines 38-41) records command **names only** — extend it to capture args so the payload shape can be asserted.

- [ ] **Step 1: Extend the command fake + add the assertion**

In `ChartController.test.ts`, augment the command fake to record args alongside names (e.g. add `const calls: Array<{ name: string; args: unknown }> = [];` and push `{ name: n, args: a }` in `sendCommand: (n, a) => {...}`). Add a test: after `addIndicator(inst)`, `calls` contains exactly one `SubscribeIndicator` with `{ instanceId, symbol, timeframe, type, params }` matching the controller's `config`.

- [ ] **Step 2: Run to verify it fails (or drives the extraction)**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: passes for the count today (behavior unchanged); the extraction below keeps it green with one construction site.

- [ ] **Step 3: Extract the helper**

Add a private method to `ChartController`:

```ts
  private subscribeIndicator(inst: IndicatorInstance): void {
    void this.deps.commands.sendCommand("SubscribeIndicator", {
      instanceId: inst.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
      type: inst.type, params: inst.params,
    });
  }
```

Replace the inline `sendCommand("SubscribeIndicator", {...})` in `addIndicator` with `this.subscribeIndicator(resolved);` and the one in `resetForReload` with `this.subscribeIndicator(inst);`.

- [ ] **Step 4: Run the test + typecheck**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/render/chart/ChartController.ts src/render/chart/ChartController.test.ts
git commit -m "refactor(ui/chart): single SubscribeIndicator payload builder"
```

---

### Task 14: Suppress session bands on D/W/M timeframes

**Files:**
- Modify: `ui/src/render/chart/ChartController.ts`
- Test: `ui/src/render/chart/ChartController.test.ts`

**Background:** `applySessions` (lines 98-103) runs for every timeframe; ET intraday shading is meaningless on Daily/Weekly/Monthly bars. `this.config.timeframe` is a plain string from `["10s","1m","5m","15m","30m","60m","D","W","M"]`. The test's fake facade `setSessionBands` is currently a bare **counter** (`facade.bands++`) — extend it to capture the passed array so emptiness can be asserted.

- [ ] **Step 1: Extend the fake + write the failing test**

In `ChartController.test.ts`, make the fake facade capture the last bands value (e.g. `setSessionBands: (b) => { facade.lastBands = b; }`). Add a test: with `timeframe: "D"` and non-empty bars, after `sync()` the facade's `lastBands` is `[]`; with `timeframe: "1m"` it's non-empty.

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: FAIL — bands are currently drawn on `"D"`.

- [ ] **Step 3: Gate `applySessions`**

In `ChartController.ts`, at the top of `applySessions`:

```ts
  private applySessions(bars: Bar[]): void {
    const intraday = !["D", "W", "M"].includes(this.config.timeframe);
    if (!intraday || bars.length === 0) { this.facade.setSessionBands([]); return; }
    const from = Date.parse(bars[0].bucketStart);
    const to = Date.parse(bars[bars.length - 1].bucketStart) + 1;
    this.facade.setSessionBands(sessionBands(from, to));
  }
```

- [ ] **Step 4: Run the test + typecheck**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/render/chart/ChartController.ts src/render/chart/ChartController.test.ts
git commit -m "fix(ui/chart): suppress ET session bands on D/W/M timeframes"
```

---

### Task 15: Indicator series `update()` fast-path

**Files:**
- Modify: `ui/src/render/chart/ChartController.ts`
- Test: `ui/src/render/chart/ChartController.test.ts`

**Background:** `applyIndicators` (lines 84-96) calls `.setData(fullArray)` on every indicator series every `sync()`. `LwcSeries.update(point)` exists (`ChartApiFacade.ts:6`) and is already used for candle/volume in `applyBars`. Add a per-series applied-count map; when a series only grew, `update()` the tail. **Do not** invent a `sameReloadKey()` method — it doesn't exist and isn't needed: clearing the count map in `resetForReload` makes the post-reload count `0`, which already forces the `setData` branch. Keep the existing `for (const { inst, series } of this.indicators.values())` destructure and the `const s = series.get(d.key)` naming. The test's `IndicatorReader` fake is `{ series: () => [] }` (always empty) — build a mutable fake (mirroring `barReaderOf`) that returns growing points.

- [ ] **Step 1: Write the failing test**

In `ChartController.test.ts`, add a mutable indicator-reader fake and a test: after an initial `sync()` (full `setData`), append one point and `sync()` again — the indicator series records an `update()` (not a second full `setData`); after `resetForReload` (symbol/timeframe change) it `setData`s the full series again. Use the file's existing `facade.created`/fake-series call-recording infrastructure.

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: FAIL — indicators always `setData`.

- [ ] **Step 3: Implement the per-series diff**

Add a field `private indicatorApplied = new Map<string, number>();`, and clear it in `resetForReload` (`this.indicatorApplied.clear();`, alongside the existing `lastAppliedCount`/`lastAppliedKey` resets). Rewrite `applyIndicators`:

```ts
  private applyIndicators(): void {
    for (const { inst, series } of this.indicators.values()) {
      const descriptors = describeIndicator(inst, this.palette);
      for (const d of descriptors) {
        const s = series.get(d.key);
        if (!s) continue;
        const points = this.deps.indicators.series(d.key);
        const applied = this.indicatorApplied.get(d.key) ?? 0;
        if (applied > 0 && points.length >= applied) {
          // Grew (or unchanged): append only the new tail. resetForReload clears
          // the map, so after a reload `applied` is 0 and we full-setData below.
          for (let i = applied; i < points.length; i++) {
            s.update({ time: toLwcTimeMs(points[i].timeMs), value: points[i].value });
          }
        } else {
          s.setData(points.map((p) => ({ time: toLwcTimeMs(p.timeMs), value: p.value })));
        }
        this.indicatorApplied.set(d.key, points.length);
      }
    }
  }
```

(`toLwcTimeMs` is the module-level helper already used here; `this.deps.indicators.series(key)` is unchanged. LWC `update()` requires non-decreasing time — points are appended in order; the `points.length >= applied` guard plus the reload-clear covers the shrink case by falling back to `setData`.)

- [ ] **Step 4: Run the chart suite + typecheck**

Run: `cd ui && npx vitest run src/render/chart/ && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/render/chart/ChartController.ts src/render/chart/ChartController.test.ts
git commit -m "perf(ui/chart): incremental update() fast-path for indicator series"
```

---

### Task 16: Palette instead of hardcoded hex (ErrorBoundary + ConnectionStatusPanel)

**Files:**
- Modify: `ui/src/chrome/ErrorBoundary.tsx`, `ui/src/chrome/panels/ConnectionStatusPanel.tsx`
- Test: `ui/src/chrome/ErrorBoundary.test.tsx`, `ui/src/chrome/ConnectionStatusPanel.test.tsx` (**both already exist**; note the Connection test lives at `src/chrome/`, not `src/chrome/panels/`)

**Background:** `ErrorBoundary.tsx:14` hardcodes `#2a1416`/`#f5b5b5`; `ConnectionStatusPanel.tsx` hardcodes dot colors (line 4), `#cbd5e1` (line 9), `#1f2430` (line 23). Map to palette keys via `useTheme()`: ok→`ok`, degraded→`warn`, bad/down→`danger`, body text→`textMuted`, divider→`border`, error text→`danger` over `surface`. `ConnectionStatusPanel`'s signature is `({ health }: { health: HealthStore })` (not `PanelProps`). `ErrorBoundary` is a **class** component — extract a functional `ErrorFallback` that calls `useTheme()`. **Both existing tests render without a `<ThemeProvider>`; adding `useTheme()` will throw unless each test wraps its render in `<ThemeProvider>`.**

- [ ] **Step 1: ConnectionStatusPanel — wire useTheme**

In `ConnectionStatusPanel.tsx`, add `import { useTheme } from "../ThemeProvider";` and `import type { Palette } from "../../render/palette";`, then `const { palette } = useTheme();` inside the component. Replace the module-level `dot` hex with a palette-derived helper and swap `#cbd5e1`→`palette.textMuted`, `#1f2430`→`palette.border`:

```ts
const dotColor = (status: string, palette: Palette) =>
  status === "ok" ? palette.ok : status === "degraded" ? palette.warn : palette.danger;
```

- [ ] **Step 2: ErrorBoundary — palette via a functional fallback**

Extract the fallback markup into a functional `ErrorFallback` that calls `useTheme()` and renders with `palette.danger` text over `palette.surface` (with a `1px solid ${palette.danger}` accent — do not invent a new hex). The class `ErrorBoundary` renders `<ErrorFallback message={...} />` in its error state.

- [ ] **Step 3: Update the existing tests to provide a theme**

Wrap the `render(...)` in both `ui/src/chrome/ErrorBoundary.test.tsx` and `ui/src/chrome/ConnectionStatusPanel.test.tsx` in `<ThemeProvider>…</ThemeProvider>` (import from `./ThemeProvider`). Assert the semantic colors resolve from the palette (import the palette constants from `../render/palette` and compare, rather than literal hex): e.g. an "ok" health row uses `getPalette("light").ok`; the error fallback uses `getPalette("light").danger`.

- [ ] **Step 4: Run tests + typecheck + grep for stray hex**

Run: `cd ui && npx vitest run src/chrome/ErrorBoundary.test.tsx src/chrome/ConnectionStatusPanel.test.tsx && npm run typecheck && grep -nE "#[0-9a-fA-F]{6}" src/chrome/ErrorBoundary.tsx src/chrome/panels/ConnectionStatusPanel.tsx`
Expected: tests + typecheck PASS; grep returns nothing.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/ErrorBoundary.tsx src/chrome/panels/ConnectionStatusPanel.tsx src/chrome/ErrorBoundary.test.tsx src/chrome/ConnectionStatusPanel.test.tsx
git commit -m "refactor(ui/chrome): palette-driven colors in ErrorBoundary + ConnectionStatusPanel"
```

---

## Out of scope (flagged, not addressed here)

- **`resolveTemplate.ts` STOP/STOP_LIMIT `stopPrice === limitPrice`** — traces to the approved UI-design spec's `PlaceOrderTemplate` single-price schema; fixing means a spec-schema change. Manual STOP_LIMIT orders are unaffected (the ticket has separate fields). Leave to a spec revision.
- **Stray `main` commit `d4a9ae4`** (content-duplicate `IndicatorStore` from a Plan-2 mistake) — harmless git hygiene; not touched here.
- **Binary-embedded `ui/dist` (`go:embed`)** — deferred to the Wails-era packaging step per the engine design; v1 serves from disk via `-dist`.
- **Wholesale fixture regeneration** — existing fixtures are shape-correct (zero drift); only a new combined fixture is captured (Task 10).

---

## Self-Review

- **Spec coverage (roadmap Plan 6 line):** Playwright smoke over engine replay/sim → Tasks 7–9; both workspaces + link focus + charts populate + ladder paints + order `→ Filled` → Task 8; `ui/dist` served by the engine → Task 3 (`-dist`) + Task 7; final error-handling matrix → Task 9. Contract swap + fixture recapture (deferred UI half of engine Plan 6) → Tasks 4, 10. Full polish sweep → Tasks 5, 6, 11–16. ✅
- **Verification pass applied (3 sonnet agents vs. real code):** engine Tasks 1–3 verified clean; UI/E2E fixes folded in — `VenueID` added to the contract adapter (Task 4); venue-arm (Task 5) reordered *before* the E2E it unblocks (Task 8); E2E submits **MARKET** (LIMIT+empty-price is client-blocked) and arms both layers; `getByLabel("focus green")` replaces the ambiguous placeholder locator; monitoring asserts an empty-state string, not canvas text; Tasks 11/12/13/14/16 corrected to target **existing** test files / add value-capturing fakes / wrap `<ThemeProvider>`; Task 15 drops the invented `sameReloadKey()`. ✅
- **Placeholder scan:** every code step carries real code. Test steps that say "reuse the file's `wrap()`/`status()` helper" are deliberate — the verifier confirmed those helpers exist and are the correct seeding mechanism (`stores.exec.apply({kind:"snapshot",topic:"exec.status",…})`, not `applyStatus`). ✅
- **Type consistency:** `markSink.SetMark(string, float64)` ↔ `sim.Broker.SetMark`; `markBridge` signature updated at its one call site; `TopicName = Topic` / `VenueID = string` aliases match every UI import; `data-testid="venue-arm-<venue>"` + `data-armed` (Task 5) and `order-type` (Task 6) produced before consumed in Task 8. ✅
