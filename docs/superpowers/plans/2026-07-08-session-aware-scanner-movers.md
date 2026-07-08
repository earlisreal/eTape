# Session-aware Scanner + Movers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make both the Scanner and Movers panels follow the currently-live trading session (pre-market → RTH → post-market → overnight), each fed by the correct per-session moomoo rank API, with Scanner keeping its filters and Movers reduced to a plain sortable board.

**Architecture:** Engine — the `scan` poller computes the session phase once per poll and fetches from the matching rank API (3410 pre-market, 3413 RTH top-movers, 3411 after-hours, 3412 overnight), normalizing each into the existing `rankItem`. A new `Overnight` phase is added to the shared `session` calendar. UI — `ScannerStore` gains `currentView()` (the session with the freshest `refreshedAt`); the single `ScannerPanel` component takes a `variant` prop instead of a fixed session and reads `currentView()`. A shared "empty seen-set ⇒ silent baseline" rule (engine + UI) suppresses the flash/chime storm at every session rollover and daily reset.

**Tech Stack:** Go 1.x + protobuf (engine), TypeScript + React + Vitest (UI). moomoo OpenD rank APIs are request/response (zero subscription quota).

## Global Constraints

- **US-only scope.** All symbols are `US.<code>`; `symbolOf`/`codeOf` in `engine/internal/scan/scan.go` already enforce this.
- **Gainers-only.** Every rank fetch uses `SortDir` descending (`proto.Int32(0)`). No losers/most-active modes.
- **Native session change ratio** ("% vs most-recent RTH close") — post-market uses `AfterHoursChangeRatio`, overnight uses `OvernightChangeRatio`; do **not** recompute against a fixed prior close.
- **Zero placeholders in shipped code.** Every rank API is request/response — no subscription quota consumed.
- **Design spec:** `docs/superpowers/specs/2026-07-08-session-aware-scanner-movers-design.md`.
- **Live-verify (not blocking these tasks; do before trusting production):** each new API's field semantics, the overnight venue window hours, and 3410's real RTH behavior — see the spec's "Live-verification items".

---

### Task 1: Add the `Overnight` session phase

**Files:**
- Modify: `engine/internal/session/session.go` (Phase enum ~line 70, `String()` ~line 77, `PhaseAt` ~line 91)
- Test: `engine/internal/session/session_test.go`

**Interfaces:**
- Produces: `session.Overnight Phase`; `session.PhaseAt(t)` returns `Overnight` for weekday times in `[20:00, 24:00) ∪ [00:00, 04:00)` ET; `Phase.String()` returns `"overnight"`.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/session/session_test.go`:

```go
func TestPhaseAtOvernight(t *testing.T) {
	loc := session.Loc()
	et := func(h, m int) time.Time { return time.Date(2026, 7, 8, h, m, 0, 0, loc) } // Wed
	cases := []struct {
		name  string
		t     time.Time
		phase session.Phase
	}{
		{"post ends 19:59", et(19, 59), session.PostMarket},
		{"overnight starts 20:00", et(20, 0), session.Overnight},
		{"overnight 23:30", et(23, 30), session.Overnight},
		{"overnight 02:00", et(2, 0), session.Overnight},
		{"overnight 03:59", et(3, 59), session.Overnight},
		{"premarket starts 04:00", et(4, 0), session.PreMarket},
		{"weekend stays closed", time.Date(2026, 7, 11, 22, 0, 0, 0, loc), session.Closed}, // Sat 22:00
	}
	for _, c := range cases {
		if got := session.PhaseAt(c.t); got != c.phase {
			t.Errorf("%s: PhaseAt=%v want %v", c.name, got, c.phase)
		}
	}
	if session.Overnight.String() != "overnight" {
		t.Errorf("Overnight.String()=%q want %q", session.Overnight.String(), "overnight")
	}
}
```

Also update the two **existing** `TestPhaseAt` cases (currently `session_test.go:71-72`) that probed the old closed gap — they now classify as `Overnight`:

```go
		{"2026-07-07T00:00:00Z", Overnight},  // 20:00 ET (was Closed)
		{"2026-07-06T07:59:59Z", Overnight},  // 03:59:59 ET (was Closed)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/session/ -run 'TestPhaseAtOvernight|TestPhaseAt'`
Expected: FAIL — `undefined: session.Overnight` (and, once that compiles, the two updated cases fail until Step 3).

- [ ] **Step 3: Add the phase, String case, and PhaseAt window**

In `session.go`, extend the const block:

```go
const (
	Closed Phase = iota
	PreMarket
	RTH
	PostMarket
	Overnight
)
```

Add the `String()` case (before the trailing `return "closed"`):

```go
	case Overnight:
		return "overnight"
```

Add the `PhaseAt` window (before the trailing `return Closed`):

```go
	case s >= 20*3600 || s < 4*3600:
		return Overnight
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/session/`
Expected: PASS (all session tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/session/session.go engine/internal/session/session_test.go
git commit -m "feat(session): add Overnight phase (20:00-04:00 ET weekdays)"
```

> **Note (documented v1 limitation, do not fix here):** the weekend guard runs first, so Friday 20:00–24:00 reads `Overnight` and Sunday 20:00–24:00 reads `Closed` — asymmetric weekend edges. Exact venue hours are a spec live-verify item.

---

### Task 2: Session-aware rank fetch in the poller

**Files:**
- Modify: `engine/internal/feed/opend/protoid.go` (add three constants after line 31)
- Modify: `engine/internal/scan/scan.go` (imports; `pollOnce` ~line 109; replace `fetchRank` ~line 195; replace `sessionOf` ~line 98)
- Test: `engine/internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `session.Phase` from Task 1; `opend.ProtoQotGetTopMoversRank=3413`, `ProtoQotGetUSAfterHoursRank=3411`, `ProtoQotGetUSOvernightRank=3412`.
- Produces: `(*Poller).fetchRank(ctx, phase session.Phase) ([]rankItem, error)`; package func `sessionKey(phase session.Phase) string` returning `"premarket"|"rth"|"afterhours"|"overnight"`.

- [ ] **Step 1: Add the protoID constants**

In `engine/internal/feed/opend/protoid.go`, after `ProtoQotGetUSPreMarketRank uint32 = 3410`:

```go
	ProtoQotGetUSAfterHoursRank uint32 = 3411
	ProtoQotGetUSOvernightRank  uint32 = 3412
	ProtoQotGetTopMoversRank    uint32 = 3413
```

- [ ] **Step 2: Write the failing test**

Extend `scan_test.go`. First widen the `fakeReq` mock to serve the new protoIDs — replace the `fakeReq` struct and its `Request` method (currently ~lines 117–146) with:

```go
// fakeReq implements the scan.requester interface with canned responses.
type fakeReq struct {
	rankResp     *rankpb.Response   // 3410 pre-market
	topMoversRsp *tmrpb.Response    // 3413 RTH
	afterHrsRsp  *ahpb.Response     // 3411 post-market
	overnightRsp *onpb.Response     // 3412 overnight
	rankErr      error
	snap         func(codes []string) (*snappb.Response, error)
	snapCalls    int
}

func (f *fakeReq) Request(_ context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	switch protoID {
	case opend.ProtoQotGetUSPreMarketRank:
		if f.rankErr != nil {
			return opend.Frame{}, f.rankErr
		}
		return frameOf(f.rankResp), nil
	case opend.ProtoQotGetTopMoversRank:
		return frameOf(f.topMoversRsp), nil
	case opend.ProtoQotGetUSAfterHoursRank:
		return frameOf(f.afterHrsRsp), nil
	case opend.ProtoQotGetUSOvernightRank:
		return frameOf(f.overnightRsp), nil
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
```

Add the imports to `scan_test.go`:

```go
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
```

Add the new test:

```go
func TestFetchRankSelectsSessionAPI(t *testing.T) {
	fr := &fakeReq{
		topMoversRsp: &tmrpb.Response{RetType: proto.Int32(0), S2C: &tmrpb.S2C{DataList: []*tmrpb.TopMoversRankItem{
			{Security: usSec("RTHX"), ChangeRatio: proto.Float64(7.5), CurPrice: proto.Float64(3.3), Volume: proto.Int64(11)}}}},
		afterHrsRsp: &ahpb.Response{RetType: proto.Int32(0), S2C: &ahpb.S2C{DataList: []*ahpb.AfterHoursRankItem{
			{Security: usSec("AHX"), AfterHoursChangeRatio: proto.Float64(4.2), AfterHoursPrice: proto.Float64(2.2), AfterHoursVolume: proto.Int64(22)}}}},
		overnightRsp: &onpb.Response{RetType: proto.Int32(0), S2C: &onpb.S2C{DataList: []*onpb.OvernightRankItem{
			{Security: usSec("ONX"), OvernightChangeRatio: proto.Float64(9.1), OvernightPrice: proto.Float64(1.1), OvernightVolume: proto.Int64(33)}}}},
		rankResp: rankResp(rankItem{Symbol: "US.PMX", ChangePct: 5.5, Last: 4.4, Volume: 44}),
	}
	p := newTestPoller(config.Scan{Enabled: true}, fr, &capturePub{})

	cases := []struct {
		phase  session.Phase
		symbol string
		pct    float64
	}{
		{session.RTH, "US.RTHX", 7.5},
		{session.PostMarket, "US.AHX", 4.2},
		{session.Overnight, "US.ONX", 9.1},
		{session.PreMarket, "US.PMX", 5.5},
		{session.Closed, "US.PMX", 5.5}, // Closed falls back to the pre-market board
	}
	for _, c := range cases {
		items, err := p.fetchRank(context.Background(), c.phase)
		if err != nil {
			t.Fatalf("phase %v: %v", c.phase, err)
		}
		if len(items) != 1 || items[0].Symbol != c.symbol || items[0].ChangePct != c.pct {
			t.Fatalf("phase %v: got %+v", c.phase, items)
		}
	}
}

func TestSessionKey(t *testing.T) {
	for phase, want := range map[session.Phase]string{
		session.RTH: "rth", session.PostMarket: "afterhours",
		session.Overnight: "overnight", session.PreMarket: "premarket", session.Closed: "premarket",
	} {
		if got := sessionKey(phase); got != want {
			t.Errorf("sessionKey(%v)=%q want %q", phase, got, want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd engine && go test ./internal/scan/ -run 'TestFetchRankSelectsSessionAPI|TestSessionKey'`
Expected: FAIL — `fetchRank` takes one arg / `sessionKey` undefined / new pb types unused compile errors.

- [ ] **Step 4: Implement the session-aware fetch**

In `scan.go`, add the imports alongside the existing `rankpb`:

```go
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
```

Replace `sessionOf` (the `func (p *Poller) sessionOf(now time.Time) string {…}` block) with a phase-keyed package func:

```go
// sessionKey maps a session phase to the scanner.rank message key. Closed
// (weekends/holidays) reuses the pre-market board.
func sessionKey(phase session.Phase) string {
	switch phase {
	case session.RTH:
		return "rth"
	case session.PostMarket:
		return "afterhours"
	case session.Overnight:
		return "overnight"
	default:
		return "premarket"
	}
}
```

Update `pollOnce` to compute the phase once and thread it through:

```go
func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	phase := session.PhaseAt(now)
	items, err := p.fetchRank(ctx, phase)
	if err != nil {
		slog.Warn("scan: rank fetch failed", "err", err)
		return // transient; next tick retries
	}
	p.resetIfNewDay(now)
	p.resolveFloats(ctx, items) // populate the float cache before filtering
	rows := rankRows(items, p.floats, p.cfg)
	sess := sessionKey(phase)
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

Replace `fetchRank` (the whole `func (p *Poller) fetchRank(ctx context.Context) …` block) with the dispatcher + four per-API helpers:

```go
// fetchRank issues the rank request for the given session phase and normalizes
// the response to []rankItem (gainers-only, SortDir descending). Each session
// uses its native change ratio (spec: "vs most-recent close").
func (p *Poller) fetchRank(ctx context.Context, phase session.Phase) ([]rankItem, error) {
	switch phase {
	case session.RTH:
		return p.fetchTopMovers(ctx)
	case session.PostMarket:
		return p.fetchAfterHours(ctx)
	case session.Overnight:
		return p.fetchOvernight(ctx)
	default: // PreMarket + Closed
		return p.fetchPreMarket(ctx)
	}
}

// gainersC2SArgs are the shared pre-market/after-hours/overnight request args
// (Market is only required by the RTH TopMovers API, set separately there).
func (p *Poller) fetchPreMarket(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank,
		&rankpb.Request{C2S: &rankpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("premarket rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(), Last: d.GetPreMarketPrice(), Volume: d.GetPreMarketVolume()})
	}
	return out, nil
}

func (p *Poller) fetchTopMovers(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetTopMoversRank,
		&tmrpb.Request{C2S: &tmrpb.C2S{
			Market:  proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), // required field
			SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp tmrpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("topmovers rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetChangeRatio(), Last: d.GetCurPrice(), Volume: d.GetVolume()})
	}
	return out, nil
}

func (p *Poller) fetchAfterHours(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSAfterHoursRank,
		&ahpb.Request{C2S: &ahpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp ahpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("afterhours rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetAfterHoursChangeRatio(), Last: d.GetAfterHoursPrice(), Volume: d.GetAfterHoursVolume()})
	}
	return out, nil
}

func (p *Poller) fetchOvernight(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSOvernightRank,
		&onpb.Request{C2S: &onpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp onpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("overnight rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetOvernightChangeRatio(), Last: d.GetOvernightPrice(), Volume: d.GetOvernightVolume()})
	}
	return out, nil
}
```

Update the package doc comment at the top of `scan.go` to mention the per-session APIs (replace the "(3410 rank, 3203 snapshot)" phrase with "(3410/3413/3411/3412 per-session rank, 3203 snapshot)").

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go test ./internal/scan/`
Expected: PASS. (`TestPollOnceEndToEnd` still passes — the fake clock is 12:00 UTC = 08:00 ET = PreMarket, which routes to `fetchPreMarket` using `rankResp`.)

- [ ] **Step 6: Commit**

```bash
git add engine/internal/feed/opend/protoid.go engine/internal/scan/scan.go engine/internal/scan/scan_test.go
git commit -m "feat(scan): fetch rank per session (RTH/after-hours/overnight APIs)"
```

---

### Task 3: Silent-baseline hit suppression (engine)

**Files:**
- Modify: `engine/internal/scan/scan.go` (`newHits` ~line 167)
- Test: `engine/internal/scan/scan_test.go` (`TestNewHitsSeenSet` ~line 105; `TestPollOnceEndToEnd` hit assertions ~line 402)

**Interfaces:**
- Produces: `(*Poller).newHits(sess string, rows []wsmsg.ScannerRow) []string` returns empty on a session's first populated poll (empty seen-set), then genuinely-new symbols thereafter.

- [ ] **Step 1: Update the failing tests to the new behavior**

Replace `TestNewHitsSeenSet` (~lines 105–115) with:

```go
func TestNewHitsSeenSet(t *testing.T) {
	p := &Poller{seen: map[string]map[string]bool{}}
	// First populated poll for a session is a silent baseline: no hits, seed only.
	first := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.B"}})
	if len(first) != 0 {
		t.Fatalf("first poll is a silent baseline, want 0 hits, got %v", first)
	}
	// Genuinely-new symbols after the baseline do fire.
	second := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.C"}})
	if len(second) != 1 || second[0] != "US.C" {
		t.Fatalf("second pass: only US.C is new, got %v", second)
	}
}
```

In `TestPollOnceEndToEnd`, update the two hit assertions. Replace the first-poll hit block (~lines 402–404):

```go
	if len(pub.hits) != 0 {
		t.Fatalf("first poll is a silent baseline -> no hits: %+v", pub.hits)
	}
```

The second-poll assertion is currently `if len(pub.hits) != 1 {` (line 411) — **change the `1` to `0`** (after the silent baseline, both polls emit zero hits) and update its message to "baseline + already-seen -> still no hits":

```go
	if len(pub.hits) != 0 {
		t.Fatalf("baseline seeded, US.LOWF already seen -> no hits on second poll: %+v", pub.hits)
	}
```

Also update the comment on ~line 406 to: `// Second poll, same board: still a rank publish, no hits (baseline already seeded).`

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/scan/ -run 'TestNewHitsSeenSet|TestPollOnceEndToEnd'`
Expected: FAIL — current `newHits` emits all symbols on the first pass.

- [ ] **Step 3: Implement the silent baseline in `newHits`**

Replace `newHits` (~lines 167–181) with:

```go
// newHits returns symbols to force-flash. A session's first populated poll
// (empty seen-set) is a silent baseline: seed the set, emit nothing — this
// avoids a whole-board flash/chime storm at session rollover and daily reset.
// Genuinely-new symbols on later polls are returned as hits.
func (p *Poller) newHits(sess string, rows []wsmsg.ScannerRow) []string {
	s := p.seen[sess]
	baseline := len(s) == 0
	if s == nil {
		s = map[string]bool{}
		p.seen[sess] = s
	}
	var hits []string
	for _, r := range rows {
		if !s[r.Symbol] {
			s[r.Symbol] = true
			if !baseline {
				hits = append(hits, r.Symbol)
			}
		}
	}
	return hits
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/scan/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/scan/scan.go engine/internal/scan/scan_test.go
git commit -m "feat(scan): silent baseline on a session's first poll (no hit storm)"
```

---

### Task 4: `ScannerStore.currentView()` + silent-baseline (UI)

**Files:**
- Modify: `ui/src/wire/contract.ts:24` (add `"overnight"`)
- Modify: `ui/src/wire/contract.test.ts:18` (widen the exact-type assertion)
- Modify: `ui/src/data/ScannerStore.ts` (`apply` ~line 25; add `currentView`; add `CurrentScannerView` type)
- Test: `ui/src/data/ScannerStore.test.ts`

**Interfaces:**
- Consumes: `ScannerSession` (now includes `"overnight"`).
- Produces: `interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null }`; `(ScannerStore).currentView(): CurrentScannerView`. `apply` treats any delta against an empty seen-set as a silent baseline (no `isNewHit`, no hit callbacks).

- [ ] **Step 1: Add the ScannerSession member**

In `ui/src/wire/contract.ts` line 24:

```ts
export type ScannerSession = "premarket" | "rth" | "afterhours" | "overnight";
```

Then widen the exact-type assertion in `ui/src/wire/contract.test.ts:18` (a compile-time `expectTypeOf(...).toEqualTypeOf(...)` that breaks `tsc --noEmit` otherwise):

```ts
    expectTypeOf<ScannerSession>().toEqualTypeOf<"premarket" | "rth" | "afterhours" | "overnight">();
```

- [ ] **Step 2: Update + add the failing tests**

In `ScannerStore.test.ts`, **update three existing tests** to the new silent-baseline semantics:

Replace the `"resetSeen re-flashes everything…"` test (~lines 46–53) with:

```ts
  it("resetSeen silently re-baselines; the next delta does not flash carried-over rows", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] }));
    s.resetSeen("premarket");
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6)] })); // empty seen ⇒ silent baseline
    expect(s.view("premarket").rows[0].isNewHit).toBe(false);
  });
```

Replace the `"sessions are isolated"` test (~lines 55–61) with:

```ts
  it("sessions are isolated (independent seen-sets)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: "t1", rows: [r("A", 2)] })); // rth baseline (silent)
    s.apply(rank("delta", "rth", { refreshedAt: "t2", rows: [r("A", 2), r("B", 9)] }));
    const rth = Object.fromEntries(s.view("rth").rows.map((row) => [row.symbol, row]));
    expect(rth.B.isNewHit).toBe(true);  // new in rth
    expect(rth.A.isNewHit).toBe(false); // carried over in rth
    expect(s.view("premarket").rows[0].isNewHit).toBe(false); // premarket untouched
  });
```

In the `onNewHit` describe block, update the `"fires for a delta row whose symbol is not yet seen"` test (~lines 83–90) expectation:

```ts
  it("first delta is a silent baseline; genuinely-new later rows fire", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("delta", ["AAA"]));        // empty seen ⇒ silent baseline
    s.apply(rankMsg("delta", ["AAA", "BBB"])); // BBB new ⇒ fires
    expect(cb.mock.calls.map((c) => c[0])).toEqual(["BBB"]);
  });
```

**Add** a `currentView` describe block at the end of the file:

```ts
describe("ScannerStore.currentView", () => {
  const iso = (h: number) => `2026-07-08T${String(h).padStart(2, "0")}:00:00.000Z`;

  it("returns null session when no data has arrived", () => {
    expect(new ScannerStore().currentView()).toEqual({ session: null, rows: [], refreshedAt: null });
  });

  it("returns the session with the freshest refreshedAt", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: iso(8), rows: [r("A", 5)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [r("B", 9)] }));
    expect(s.currentView().session).toBe("rth");
    expect(s.currentView().rows[0].symbol).toBe("B");
  });

  it("follows the rollover as a newer session overtakes", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [r("B", 9)] }));
    expect(s.currentView().session).toBe("rth");
    s.apply(rank("snapshot", "afterhours", { refreshedAt: iso(17), rows: [r("C", 4)] }));
    expect(s.currentView().session).toBe("afterhours");
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts`
Expected: FAIL — `currentView` undefined; updated assertions fail against current `apply`.

- [ ] **Step 4: Implement the silent baseline + currentView**

In `ScannerStore.ts`, update the exports near the top (after the existing interfaces):

```ts
export interface CurrentScannerView { session: ScannerSession | null; rows: ScannerRowView[]; refreshedAt: string | null }
```

Replace the body of `apply` from the `const seen = this.seenFor(session);` line through the `this.setSession(...)` call with:

```ts
    const seen = this.seenFor(session);
    if (m.kind === "snapshot") seen.clear(); // a (re)snapshot is a fresh baseline
    // A delta against an empty seen-set is a session's first board (rollover,
    // fresh session start, or post-reset): seed it silently so the whole board
    // does not flash/chime at once. Genuinely-new symbols flash on later deltas.
    const isBaseline = m.kind === "snapshot" || seen.size === 0;
    const newHits: string[] = [];
    const view: ScannerRowView[] = rows.map((row) => {
      const isNewHit = !isBaseline && !seen.has(row.symbol);
      const muted = !isBaseline && seen.has(row.symbol);
      if (isNewHit) newHits.push(row.symbol);
      return { ...row, isNewHit, muted };
    });
    for (const row of rows) seen.add(row.symbol);
    this.setSession(session, { rows: view, refreshedAt });
```

(The `for (const symbol of newHits)` callback loop below it is unchanged.)

Add the `currentView` method (e.g. right after `view`):

```ts
  // The session view with the freshest refreshedAt — the "live" board the
  // panels follow. Null session until any data arrives.
  currentView(): CurrentScannerView {
    const sessions = this.getSnapshot().sessions;
    let best: ScannerSession | null = null;
    let bestT = -Infinity;
    for (const key of Object.keys(sessions) as ScannerSession[]) {
      const v = sessions[key];
      if (!v?.refreshedAt) continue;
      const t = Date.parse(v.refreshedAt);
      const ms = Number.isNaN(t) ? -Infinity : t;
      if (ms > bestT) { bestT = ms; best = key; }
    }
    if (!best) return { session: null, rows: [], refreshedAt: null };
    const v = sessions[best]!;
    return { session: best, rows: v.rows, refreshedAt: v.refreshedAt };
  }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/src/wire/contract.ts ui/src/data/ScannerStore.ts ui/src/data/ScannerStore.test.ts
git commit -m "feat(ui): ScannerStore.currentView + silent-baseline rank deltas"
```

---

### Task 5: `ScannerPanel` variant + registry wiring

**Files:**
- Modify: `ui/src/chrome/panels/ScannerPanel.tsx` (signature ~line 50; `SESSION_LABEL` ~line 12; data read ~line 55; header ~line 91; filter UI ~lines 100–130; midnight reset ~line 64)
- Modify: `ui/src/chrome/panels/registry.tsx` (`scanner` ~line 100, `movers` ~line 108)
- Test: `ui/src/chrome/panels/ScannerPanel.test.tsx`

**Interfaces:**
- Consumes: `stores.scanner.currentView()` and `CurrentScannerView` from Task 4.
- Produces: `ScannerPanel` takes `variant: "scanner" | "movers"` (replaces `session`). `variant==="movers"` renders no filter popover/summary and applies no thresholds.

- [ ] **Step 1: Update the failing tests**

In `ScannerPanel.test.tsx`, change `renderPanel` to pass `variant` and use ISO timestamps. Replace the props cast + render (~lines 25–27) with:

```ts
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async () => ({ status: "accepted" }) } } as unknown as PanelProps & { variant: "scanner" | "movers" };
  render(<ThemeProvider><ScannerPanel {...props} variant={variant} /></ThemeProvider>);
```

Change the `renderPanel` signature (~line 16) to:

```ts
function renderPanel(over: Partial<PanelConfig> = {}, variant: "scanner" | "movers" = "scanner") {
```

Replace every `refreshedAt: "t"` and `refreshedAt: "2026-07-06T13:30:00Z"` in this file with `refreshedAt: "2026-07-08T13:00:00.000Z"` (currentView requires a parseable timestamp to select the session).

Add two new tests inside the `describe("ScannerPanel", …)` block:

```ts
  it("movers variant has no filter button and applies no thresholds", () => {
    const { scanner } = renderPanel(
      { settings: { targetGroup: "green", thresholds: { minChangePct: 50, floatCapShares: null, minVolume: 0 } } },
      "movers",
    );
    expect(screen.queryByRole("button", { name: /filters/i })).toBeNull();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth",
      payload: { refreshedAt: "2026-07-08T14:00:00.000Z", rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 }] } }));
    expect(screen.getByText("US.LOW")).toBeTruthy(); // not filtered out despite minChangePct:50
  });

  it("follows the live session label", () => {
    const { scanner } = renderPanel({}, "movers");
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "afterhours",
      payload: { refreshedAt: "2026-07-08T21:00:00.000Z", rows: [
        { symbol: "US.AH", changePct: 3, last: 1, floatShares: 1, volume: 1 }] } }));
    expect(screen.getByText(/after-hours/i)).toBeTruthy();
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/panels/ScannerPanel.test.tsx`
Expected: FAIL — `ScannerPanel` still expects a `session` prop; movers/label tests fail.

- [ ] **Step 3: Implement the variant panel**

In `ScannerPanel.tsx`:

Add the overnight label to `SESSION_LABEL` (~line 12):

```ts
const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH movers", afterhours: "After-hours", overnight: "Overnight",
};
```

Change the component signature (~line 50–51):

```ts
export function ScannerPanel(
  { config, stores, linkGroups, onConfigChange, variant }: PanelProps & { variant: "scanner" | "movers" },
): JSX.Element {
```

Replace the data read (~line 55) — drop the fixed-session `view` for `currentView`:

```ts
  const cv = useMemo(() => stores.scanner.currentView(), [snap, stores.scanner]);
```

Replace the midnight-reset effect (~lines 64–69) to reset all sessions:

```ts
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner]);
```

Replace the `rows` memo (~lines 71–74) so movers applies no thresholds:

```ts
  const rows = useMemo(
    () => sortRows(applyScannerFilters(cv.rows, variant === "movers" ? DEFAULT_THRESHOLDS : thresholds), sort, SORT_ACCESSORS),
    [cv.rows, thresholds, sort, variant],
  );
```

Replace the header (~lines 91–93). Test `cv.refreshedAt` (not `cv.session`) in the ternary so TypeScript narrows it to `string` for `formatTapeTime`; `cv.session` is non-null whenever `refreshedAt` is set (both come from the same branch in `currentView`), so assert it:

```ts
  const header = cv.refreshedAt
    ? `${SESSION_LABEL[cv.session!]} · updated ${formatTapeTime(cv.refreshedAt)}`
    : "Waiting for scanner data…";
```

Wrap the filter button (the `<button … aria-label="filters" …>` … `</button>` at ~lines 100–103) and the popover (`{filtersOpen && (…)}` at ~lines 111–126) and the summary row (the `<div className="mono" …>{formatFilterSummary(thresholds)}</div>` at ~lines 128–130) each in `{variant === "scanner" && ( … )}`.

Replace the one remaining `sv.refreshedAt` reference (empty-state row check, line 155) with `cv.refreshedAt` (the header replacement above already covers the two at lines 91–92).

- [ ] **Step 4: Update the registry**

In `registry.tsx`, replace the `scanner` and `movers` entries' `component` and `description`:

```tsx
  "scanner": {
    component: (p) => <ScannerPanel {...p} variant="scanner" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Scanner",
    glyph: "%",
    description: "Live gappers, all sessions, filters",
    symbolBearing: false,
  },
  "movers": {
    component: (p) => <ScannerPanel {...p} variant="movers" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Movers",
    glyph: "↕",
    description: "Live % leaders, all sessions",
    symbolBearing: false,
  },
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/panels/ScannerPanel.test.tsx src/chrome/panels/registry.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/ScannerPanel.tsx ui/src/chrome/panels/registry.tsx ui/src/chrome/panels/ScannerPanel.test.tsx
git commit -m "feat(ui): session-following Scanner + filterless Movers via variant prop"
```

---

## Verification

**Full test + typecheck:**

- [ ] Engine: `cd engine && go build ./... && go test ./internal/session/ ./internal/scan/`
- [ ] UI unit (run the scanner files individually — batched forks-pool reuse can cross-contaminate canvas-touching suites): `cd ui && npx vitest run src/data/ScannerStore.test.ts && npx vitest run src/chrome/panels/ScannerPanel.test.tsx && npx vitest run src/chrome/panels/registry.test.tsx`
- [ ] UI typecheck: `cd ui && npx tsc --noEmit`
- [ ] Wire-type drift gate (ScannerSession is a UI-side convention, not generated, so this should stay green): `cd engine && make gen-ts-check`

**End-to-end against a running OpenD (the real proof — pick the window you can test in):**

- [ ] Start the engine with the scanner enabled and open a Scanner + Movers panel in the UI.
- [ ] **During RTH:** confirm the board shows live intraday `ChangeRatio` values that move over successive polls (not frozen pre-market numbers), header reads "RTH movers", and Movers has no ⚙ filters button while Scanner does.
- [ ] **Across 09:30 (if testable):** confirm both panels auto-swap from "Pre-market" to "RTH movers" within one poll, with no whole-board amber flash or chime burst.
- [ ] **Post-market / overnight (if testable):** confirm the header relabels and rows reflect the after-hours / overnight change ratios; verify the overnight window matches the venue's real hours (spec live-verify item — adjust the `20:00`/`04:00` constants in `session.go` if OpenD disagrees).

## Spec coverage self-check

- Auto-follow live session → Task 4 (`currentView`) + Task 5 (panel reads it). ✅
- Live per-session data (premarket/RTH/post/overnight APIs) → Task 2. ✅
- New Overnight phase + `"overnight"` ScannerSession → Task 1 + Task 4. ✅
- Native session change ratio → Task 2 (uses `*ChangeRatio` getters, no recompute). ✅
- Scanner keeps filters; Movers plain sortable board → Task 5. ✅
- Suppress rollover/reset flash + chime storm → Task 3 (engine hits) + Task 4 (UI rank/flash). ✅
- Gainers-only → Global Constraints + every fetch uses `SortDir` desc. ✅
- Out of scope (losers/most-active, manual session picker, rank_pages paging) → not implemented, per spec. ✅
