package uihub

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func testMirror() *mirror {
	return newMirror(
		[]venueMeta{{ID: "sim", Broker: wsmsg.BrokerAlpaca, Gate: wsmsg.GateLimitsView{MaxOrderValue: 1000}}},
		wsmsg.GlobalLimitsView{MaxDayLoss: 500},
		200, 200, 500, 500,
	)
}

func TestMirrorQuoteJoinsBookBidAsk(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BookUpdate{Book: feed.Book{Symbol: "US.AAPL", TsMs: 1,
		Bids: []feed.BookLevel{{Price: 3.46, Volume: 100}},
		Asks: []feed.BookLevel{{Price: 3.48, Volume: 120}}}})
	d := m.applyMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 2}})
	if len(d) != 1 || d[0].Topic != wsmsg.TopicQuote {
		t.Fatalf("expected one quote delta, got %v", d)
	}
	q := d[0].Payload.(wsmsg.Quote)
	if q.Bid != 3.46 || q.Ask != 3.48 || q.Last != 3.47 {
		t.Fatalf("bid/ask join failed: %+v", q)
	}
}

func TestMirrorBarsSeriesUpsertAndSnapshot(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 1, InProgress: true}})
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 2, InProgress: false}}) // finalize same bucket
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 120_000, C: 3, InProgress: true}}) // new bucket
	frames := m.snapshotFrames(wsmsg.TopicBars)
	if len(frames) != 1 {
		t.Fatalf("expected one bars snapshot frame (one series), got %d", len(frames))
	}
	series := frames[0].Payload.([]wsmsg.Bar)
	if len(series) != 2 {
		t.Fatalf("expected 2 bars (upserted 60s + new 120s), got %d: %+v", len(series), series)
	}
	if series[0].C != 2 || !(series[1].C == 3) {
		t.Fatalf("bar upsert/append wrong: %+v", series)
	}
}

// TestMirrorBarSnapshotFullReplace verifies md.BarSnapshot (the lossless
// history-seed replacement for per-bar BarUpdate emission) fully replaces the
// series in one shot -- including overwriting a stale bucket left by an
// earlier per-bar delta -- rather than upserting into it.
func TestMirrorBarSnapshotFullReplace(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 1, InProgress: true}})
	seed := []md.Bar{
		{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 2},
		{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 120_000, C: 3},
		{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 180_000, C: 4},
	}
	d := m.applyMD(md.BarSnapshot{Symbol: "US.AAPL", TF: session.TF1m, Bars: seed})
	if len(d) != 1 || d[0].Topic != wsmsg.TopicBars || !d[0].Snap {
		t.Fatalf("expected one Snap-flagged bars staged frame, got %+v", d)
	}
	payload := d[0].Payload.([]wsmsg.Bar)
	if len(payload) != 3 {
		t.Fatalf("staged snapshot payload = %d bars, want 3", len(payload))
	}
	frames := m.snapshotFrames(wsmsg.TopicBars)
	if len(frames) != 1 {
		t.Fatalf("expected one bars snapshot frame (one series), got %d", len(frames))
	}
	series := frames[0].Payload.([]wsmsg.Bar)
	if len(series) != 3 {
		t.Fatalf("BarSnapshot did not fully replace the series: %d bars, want 3", len(series))
	}
	if series[0].C != 2 {
		t.Fatalf("BarSnapshot did not overwrite the stale bucket: %+v", series[0])
	}
}

// TestMirrorBarSnapshotEmptyIsNoop verifies an empty BarSnapshot (e.g. a
// timeframe seedHistory1m touched but is still empty) produces no frame and
// leaves any existing series untouched.
func TestMirrorBarSnapshotEmptyIsNoop(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 1}})
	d := m.applyMD(md.BarSnapshot{Symbol: "US.AAPL", TF: session.TF1m, Bars: nil})
	if d != nil {
		t.Fatalf("empty BarSnapshot produced a staged frame: %+v", d)
	}
	series := m.bars[barKey("US.AAPL", string(session.TF1m))]
	if len(series) != 1 {
		t.Fatalf("empty BarSnapshot touched the existing series: %+v", series)
	}
}

func TestMirrorExecStatusAggregate(t *testing.T) {
	m := testMirror()
	m.applyExec(exec.StatusUpdate{Venue: "sim", Connected: true, MasterArmed: false, Note: "up"})
	d := m.applyExec(exec.AccountUpdate{
		Account:    exec.AccountSnapshot{Venue: "sim", Equity: 100000, DayPnL: -50, TsMs: 5},
		VenueArmed: true, MasterArmed: true,
	})
	// AccountUpdate produces both an exec.account delta and an exec.status delta.
	var accountSeen, statusSeen bool
	for _, s := range d {
		switch s.Topic {
		case wsmsg.TopicExecAccount:
			accountSeen = true
		case wsmsg.TopicExecStatus:
			st := s.Payload.(wsmsg.ExecStatus)
			if !st.MasterArmed || len(st.Venues) != 1 || !st.Venues[0].VenueArmed || !st.Venues[0].Connected {
				t.Fatalf("exec.status aggregate wrong: %+v", st)
			}
			if st.Global.MaxDayLoss != 500 || st.Venues[0].Gate.MaxOrderValue != 1000 {
				t.Fatalf("gate limits not merged from config: %+v", st)
			}
			statusSeen = true
		}
	}
	if !accountSeen || !statusSeen {
		t.Fatalf("AccountUpdate must yield account+status deltas; got %v", d)
	}
}

func TestMirrorPositionsSnapshotUsesMark(t *testing.T) {
	m := testMirror()
	m.applyMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.60, TsMs: 1}}) // sets mark
	m.applyExec(exec.PositionUpdate{Position: exec.Position{Venue: "sim", Symbol: "US.AAPL", Qty: 100, AvgPrice: 3.50}})
	frames := m.snapshotFrames(wsmsg.TopicExecPositions)
	if len(frames) != 1 {
		t.Fatalf("positions snapshot is one full-replace frame, got %d", len(frames))
	}
	rows := frames[0].Payload.([]wsmsg.PositionRow)
	if len(rows) != 1 || rows[0].UnrealizedPnl < 9.99 || rows[0].UnrealizedPnl > 10.01 {
		t.Fatalf("position pnl from mark wrong: %+v", rows)
	}
}

func TestMirrorOrdersSnapshotIsArray(t *testing.T) {
	m := testMirror()
	m.applyExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Symbol: "US.AAPL", Status: exec.StatusSubmitted}})
	m.applyExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET2", Symbol: "US.AAPL", Status: exec.StatusAccepted}})
	frames := m.snapshotFrames(wsmsg.TopicExecOrders)
	if len(frames) != 1 {
		t.Fatalf("orders snapshot is a single Order[] frame, got %d", len(frames))
	}
	if got := len(frames[0].Payload.([]wsmsg.Order)); got != 2 {
		t.Fatalf("expected 2 orders, got %d", got)
	}
}

func TestMirrorApplyPubNewsHealthEvents(t *testing.T) {
	m := testMirror()
	m.applyPub(staged{Topic: wsmsg.TopicNews, Payload: wsmsg.NewsItem{Symbol: "US.AAPL", Headline: "one"}})
	m.applyPub(staged{Topic: wsmsg.TopicNews, Payload: []wsmsg.NewsItem{{Symbol: "US.AAPL", Headline: "two"}, {Symbol: "US.AAPL", Headline: "three"}}})
	m.applyPub(staged{Topic: wsmsg.TopicSysHealth, Payload: wsmsg.HealthSnapshot{Links: []wsmsg.HealthLink{{Link: wsmsg.LinkUIEngine, Status: wsmsg.LinkOK}}}})
	m.applyPub(staged{Topic: wsmsg.TopicSysEvents, Payload: wsmsg.SysEvent{Seq: 1, Kind: "boot"}})
	m.applyPub(staged{Topic: wsmsg.TopicSysEvents, Payload: []wsmsg.SysEvent{{Seq: 2, Kind: "resync"}, {Seq: 3, Kind: "gap"}}})
	m.applyPub(staged{Topic: wsmsg.TopicScannerRank, Key: "am", Payload: wsmsg.ScannerRankPayload{RefreshedAt: "now"}})

	newsFrames := m.snapshotFrames(wsmsg.TopicNews)
	if len(newsFrames) != 1 || len(newsFrames[0].Payload.([]wsmsg.NewsItem)) != 3 {
		t.Fatalf("expected 3 accumulated news items, got %+v", newsFrames)
	}
	healthFrames := m.snapshotFrames(wsmsg.TopicSysHealth)
	if len(healthFrames) != 1 || len(healthFrames[0].Payload.(wsmsg.HealthSnapshot).Links) != 1 {
		t.Fatalf("expected health snapshot recorded, got %+v", healthFrames)
	}
	eventFrames := m.snapshotFrames(wsmsg.TopicSysEvents)
	if len(eventFrames) != 1 || len(eventFrames[0].Payload.([]wsmsg.SysEvent)) != 3 {
		t.Fatalf("expected 3 accumulated sys events, got %+v", eventFrames)
	}
	rankFrames := m.snapshotFrames(wsmsg.TopicScannerRank)
	if len(rankFrames) != 1 || rankFrames[0].Key != "am" {
		t.Fatalf("expected scanner rank recorded under key 'am', got %+v", rankFrames)
	}
}

func TestMirrorEmptyNewsSnapshotMarshalsToArrayNotNull(t *testing.T) {
	m := testMirror()
	// A brand-new subscriber gets a news snapshot before any news exists. The
	// payload must serialize to a JSON array `[]`, not `null` — a nil Go slice
	// marshals to null, which crashes the UI NewsStore's dedup (reading .url of null).
	frames := m.snapshotFrames(wsmsg.TopicNews)
	if len(frames) != 1 {
		t.Fatalf("expected exactly one news snapshot frame, got %d", len(frames))
	}
	b, err := json.Marshal(frames[0].Payload)
	if err != nil {
		t.Fatalf("marshal news payload: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("empty news snapshot must marshal to []: got %s", b)
	}
}

func TestNewMirrorSeedsAutoArmAndNote(t *testing.T) {
	m := newMirror([]venueMeta{
		{ID: "alpaca-paper", Broker: wsmsg.BrokerAlpaca, AutoArm: true},
		{ID: "alpaca-live", Broker: wsmsg.BrokerAlpaca},
		{ID: "moomoo", Broker: wsmsg.BrokerMoomoo, Note: "execution v1.x"},
	}, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)

	st := m.execStatus()
	if !st.MasterArmed {
		t.Fatal("master should seed armed when a venue auto-arms")
	}
	byID := map[string]wsmsg.VenueStatus{}
	for _, v := range st.Venues {
		byID[v.Venue] = v
	}
	if !byID["alpaca-paper"].VenueArmed {
		t.Fatal("alpaca-paper should seed armed")
	}
	if byID["alpaca-live"].VenueArmed {
		t.Fatal("alpaca-live should seed disarmed")
	}
	if byID["moomoo"].Connected {
		t.Fatal("moomoo stub should seed disconnected")
	}
	if byID["moomoo"].Note != "execution v1.x" {
		t.Fatalf("moomoo note wrong: %q", byID["moomoo"].Note)
	}
}

func TestMirrorStatusUpdateDoesNotClobberSeededNote(t *testing.T) {
	m := newMirror([]venueMeta{
		{ID: "moomoo", Broker: wsmsg.BrokerMoomoo, Note: "execution v1.x"},
	}, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)

	// Seeded venue starts disconnected; flip it to connected first so the
	// later "back to false" transition below actually proves Connected is
	// being live-updated from the StatusUpdate, not just matching a zero value.
	m.applyExec(exec.StatusUpdate{Venue: "moomoo", Connected: true, MasterArmed: false})
	if v := m.execStatus().Venues[0]; !v.Connected || v.Note != "execution v1.x" {
		t.Fatalf("after Connected:true update: got %+v", v)
	}

	// exec.Core.emitStatus() sends StatusUpdate with a zero-value Note on every
	// arm/disarm/kill — this must not wipe the seeded per-venue note, since a
	// venue's descriptive note is static config, not something the exec engine
	// dynamically changes.
	m.applyExec(exec.StatusUpdate{Venue: "moomoo", Connected: false, MasterArmed: false})

	st := m.execStatus()
	if len(st.Venues) != 1 {
		t.Fatalf("expected 1 venue, got %d", len(st.Venues))
	}
	v := st.Venues[0]
	if v.Note != "execution v1.x" {
		t.Fatalf("StatusUpdate with empty Note must not clobber seeded note: got %q", v.Note)
	}
	if v.Connected {
		t.Fatalf("Connected should reflect the incoming StatusUpdate (false), got true — Connected update broken")
	}
}

func TestMirrorNewsAndEventsCapBounded(t *testing.T) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 2, 500, 2)
	for i := 0; i < 5; i++ {
		m.applyPub(staged{Topic: wsmsg.TopicNews, Payload: wsmsg.NewsItem{Headline: string(rune('a' + i))}})
		m.applyPub(staged{Topic: wsmsg.TopicSysEvents, Payload: wsmsg.SysEvent{Seq: int64(i)}})
	}
	if len(m.news) != 2 {
		t.Fatalf("expected news ring capped at 2, got %d", len(m.news))
	}
	if len(m.events) != 2 {
		t.Fatalf("expected events ring capped at 2, got %d", len(m.events))
	}
	if m.events[1].Seq != 4 {
		t.Fatalf("expected most recent event retained, got %+v", m.events)
	}
}
