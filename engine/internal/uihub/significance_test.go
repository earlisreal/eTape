package uihub

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func significanceTestTick(seq, size int64, at time.Time, typ feed.TransactionType) feed.Tick {
	return feed.Tick{
		Symbol: "US.AAPL", Seq: seq, TsMs: at.UnixMilli(), Price: 100,
		Volume: size, Dir: feed.Neutral, Type: typ,
	}
}

func significanceTapeAndStatus(m *mirror, ticks ...feed.Tick) ([]wsmsg.Tick, *wsmsg.SignificanceStatus) {
	frames := m.applyMD(md.TapeUpdate{Symbol: "US.AAPL", Ticks: ticks})
	var tape []wsmsg.Tick
	var status *wsmsg.SignificanceStatus
	for _, frame := range frames {
		switch p := frame.Payload.(type) {
		case []wsmsg.Tick:
			tape = p
		case wsmsg.SignificanceStatus:
			copy := p
			status = &copy
		}
	}
	return tape, status
}

func TestMirrorSignificanceActivatesLargeAt200PrecedingPrints(t *testing.T) {
	m := testMirror()
	at := time.Date(2026, time.July, 6, 10, 0, 0, 0, session.Loc())
	for seq := int64(1); seq <= 199; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, at.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	_, status := significanceTapeAndStatus(m, significanceTestTick(200, 100, at.Add(200*time.Millisecond), feed.TransactionRegular))
	if status == nil || status.BaselineCount != 200 || !status.LargeAvailable || status.LargeThreshold != 300 {
		t.Fatalf("activation status = %+v, want 200-count large threshold 300", status)
	}
	tape, _ := significanceTapeAndStatus(m, significanceTestTick(201, 300, at.Add(201*time.Millisecond), feed.TransactionRegular))
	if len(tape) != 1 || tape[0].Significance != wsmsg.SignificanceLarge {
		t.Fatalf("next print = %+v, want Large", tape)
	}
}

func TestMirrorSignificanceActivatesExceptionalAt1000PrecedingPrints(t *testing.T) {
	m := testMirror()
	at := time.Date(2026, time.July, 6, 10, 0, 0, 0, session.Loc())
	for seq := int64(1); seq <= 999; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, at.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	_, status := significanceTapeAndStatus(m, significanceTestTick(1000, 100, at.Add(1000*time.Millisecond), feed.TransactionRegular))
	if status == nil || status.BaselineCount != 1000 || !status.ExceptionalAvailable || status.ExceptionalThreshold != 800 {
		t.Fatalf("activation status = %+v, want 1000-count exceptional threshold 800", status)
	}
	tape, _ := significanceTapeAndStatus(m, significanceTestTick(1001, 800, at.Add(1001*time.Millisecond), feed.TransactionRegular))
	if len(tape) != 1 || tape[0].Significance != wsmsg.SignificanceExceptional {
		t.Fatalf("next print = %+v, want Exceptional", tape)
	}
}

func TestMirrorSignificanceSweepsScoreButDoNotTeach(t *testing.T) {
	m := testMirror()
	at := time.Date(2026, time.July, 6, 10, 0, 0, 0, session.Loc())
	for seq := int64(1); seq <= 200; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, at.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	tape, _ := significanceTapeAndStatus(m, significanceTestTick(201, 1_000, at.Add(201*time.Millisecond), feed.TransactionIntermarketSweep))
	if len(tape) != 1 || tape[0].Significance != wsmsg.SignificanceLarge {
		t.Fatalf("sweep = %+v, want Large against existing threshold", tape)
	}
	for _, typ := range []feed.TransactionType{feed.TransactionExcluded, feed.TransactionUnknown} {
		tape, _ = significanceTapeAndStatus(m, significanceTestTick(202, 1_000, at.Add(202*time.Millisecond), typ))
		if len(tape) != 1 || tape[0].Significance != wsmsg.SignificanceNone {
			t.Fatalf("excluded type %v = %+v, want none", typ, tape)
		}
	}
	for seq := int64(203); seq <= 265; seq++ {
		typ := feed.TransactionRegular
		if seq == 203 {
			typ = feed.TransactionOddLot
		}
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, at.Add(time.Duration(seq)*time.Millisecond), typ))
	}
	_, status := significanceTapeAndStatus(m, significanceTestTick(266, 100, at.Add(266*time.Millisecond), feed.TransactionRegular))
	if status == nil || status.BaselineCount != 264 {
		t.Fatalf("post-cadence status = %+v, want 264 learned prints (sweep/excluded/unknown skipped)", status)
	}
}

func TestMirrorSignificanceUsesIndependentPoolsAndStampsHistory(t *testing.T) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 1_000, 200, 500, 500, 500)
	rth := time.Date(2026, time.July, 6, 10, 0, 0, 0, session.Loc())
	for seq := int64(1); seq <= 200; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, rth.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	large, _ := significanceTapeAndStatus(m, significanceTestTick(201, 300, rth.Add(201*time.Millisecond), feed.TransactionRegular))
	if len(large) != 1 || large[0].Significance != wsmsg.SignificanceLarge {
		t.Fatalf("RTH print = %+v, want Large", large)
	}

	extended := rth.Add(7 * time.Hour)
	_, status := significanceTapeAndStatus(m, significanceTestTick(202, 100, extended, feed.TransactionRegular))
	if status == nil || status.Pool != wsmsg.SignificancePoolExtended || status.BaselineCount != 1 {
		t.Fatalf("extended status = %+v, want independent one-print pool", status)
	}
	for seq := int64(203); seq <= 400; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, extended.Add(time.Duration(seq-202)*time.Millisecond), feed.TransactionRegular))
	}
	_, status = significanceTapeAndStatus(m, significanceTestTick(401, 100, extended.Add(199*time.Millisecond), feed.TransactionRegular))
	if status == nil || status.BaselineCount != 200 {
		t.Fatalf("extended activation status = %+v, want 200", status)
	}
	if got := m.snapshotFrames(wsmsg.TopicTape)[0].Payload.([]wsmsg.Tick)[200].Significance; got != wsmsg.SignificanceLarge {
		t.Fatalf("historical RTH significance changed to %q", got)
	}
}

func TestMirrorSignificanceClosedStatusPreservesThresholdsAndRolloverWarms(t *testing.T) {
	m := testMirror()
	rth := time.Date(2026, time.November, 25, 21, 0, 0, 0, session.Loc())
	for seq := int64(1); seq <= 200; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, rth.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	closed := m.advanceSignificance(time.Date(2026, time.November, 26, 10, 0, 0, 0, session.Loc()))
	if len(closed) != 1 {
		t.Fatalf("closed transition emitted %d statuses, want 1", len(closed))
	}
	if got := closed[0].Payload.(wsmsg.SignificanceStatus); got.State != wsmsg.SignificanceStateClosed || got.BaselineCount != 200 || !got.LargeAvailable {
		t.Fatalf("closed status = %+v, want retained RTH thresholds", got)
	}

	rollover := m.advanceSignificance(time.Date(2026, time.November, 27, 20, 0, 0, 0, session.Loc()))
	if len(rollover) != 1 {
		t.Fatalf("rollover emitted %d statuses, want 1", len(rollover))
	}
	if got := rollover[0].Payload.(wsmsg.SignificanceStatus); got.State != wsmsg.SignificanceStateWarming || got.BaselineCount != 0 || got.LargeAvailable {
		t.Fatalf("rollover status = %+v, want zero-count warming", got)
	}
}

func TestMirrorSignificanceAllowsSequenceResetAfterCalendarMidnight(t *testing.T) {
	m := testMirror()
	start := time.Date(2026, time.July, 6, 20, 30, 0, 0, session.Loc())
	for seq := int64(1_000); seq < 1_200; seq++ {
		significanceTapeAndStatus(m, significanceTestTick(seq, 100, start.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	first, _ := significanceTapeAndStatus(m, significanceTestTick(1, 300, start.Add(201*time.Millisecond), feed.TransactionRegular))
	if len(first) != 1 || first[0].Significance != wsmsg.SignificanceLarge {
		t.Fatalf("first sequence-1 print = %+v, want Large", first)
	}

	second, _ := significanceTapeAndStatus(m, significanceTestTick(1, 100, time.Date(2026, time.July, 7, 0, 30, 0, 0, session.Loc()), feed.TransactionRegular))
	if len(second) != 1 || second[0].Significance != wsmsg.SignificanceNone {
		t.Fatalf("next-day sequence-1 print = %+v, want None", second)
	}
}

func TestMirrorSignificancePublishesFullWindowStatus(t *testing.T) {
	m := testMirror()
	at := time.Date(2026, time.July, 6, 10, 0, 0, 0, session.Loc())
	ticks := make([]feed.Tick, 0, significanceWindow)
	for seq := int64(1); seq <= significanceWindow; seq++ {
		ticks = append(ticks, significanceTestTick(seq, 100, at.Add(time.Duration(seq)*time.Millisecond), feed.TransactionRegular))
	}
	frames := m.applyMD(md.TapeUpdate{Symbol: "US.AAPL", Ticks: ticks})
	var status *wsmsg.SignificanceStatus
	for _, frame := range frames {
		if value, ok := frame.Payload.(wsmsg.SignificanceStatus); ok {
			copy := value
			status = &copy
		}
	}
	if status == nil || !status.Full || status.Provisional || status.BaselineCount != significanceWindow {
		t.Fatalf("full-window status = %+v, want full 2000-count status", status)
	}
}
