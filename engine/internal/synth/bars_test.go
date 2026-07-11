package synth

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestBarAgg_OHLCContinuityAndClose(t *testing.T) {
	var ba barAgg
	mk := func(ts int64, px float64, v int64) feed.Tick {
		return feed.Tick{Symbol: "US.TST", TsMs: ts, Price: px, Volume: v, Turnover: px * float64(v)}
	}
	var closed *feed.Bar
	for _, tk := range []feed.Tick{
		mk(60_000, 10.00, 100), mk(60_500, 10.20, 50), mk(61_000, 9.90, 30), // minute 1
		mk(120_000, 10.10, 20), // minute 2: triggers the minute-1 close
	} {
		if c := ba.add(tk); c != nil {
			closed = c
		}
	}
	if closed == nil {
		t.Fatal("expected minute-1 bar to close when minute-2 tick arrived")
	}
	if closed.O != 10.00 || closed.H != 10.20 || closed.L != 9.90 {
		t.Errorf("bad OHLC: %+v", *closed)
	}
	if closed.Volume != 180 {
		t.Errorf("volume %d want 180", closed.Volume)
	}
	if closed.BucketMs != 60_000 {
		t.Errorf("bucket %d want 60000", closed.BucketMs)
	}
}

func TestBuildQuote_PrevCloseAndCumulative(t *testing.T) {
	sess := &sessionAgg{Open: 10, High: 11, Low: 9.5, Last: 10.8, Vol: 1234, Turnover: 13000, hasOpen: true}
	q := buildQuote("US.TST", sess, 9.0, 123)
	if q.PrevClose != 9.0 || q.Last != 10.8 || q.Volume != 1234 {
		t.Errorf("bad quote: %+v", q)
	}
}

func TestBarAgg_InProgress(t *testing.T) {
	var ba barAgg
	if _, ok := ba.inProgress("US.TST"); ok {
		t.Fatal("expected no in-progress bar before any tick")
	}

	tk := feed.Tick{Symbol: "US.TST", TsMs: 60_000, Price: 10.00, Volume: 100, Turnover: 1000}
	if closed := ba.add(tk); closed != nil {
		t.Fatalf("first tick should not close a bar, got %+v", *closed)
	}

	wip, ok := ba.inProgress("US.TST")
	if !ok {
		t.Fatal("expected an in-progress bar after one tick")
	}
	if wip.Symbol != "US.TST" || wip.BucketMs != 60_000 || wip.O != 10.00 || wip.H != 10.00 || wip.L != 10.00 || wip.C != 10.00 || wip.Volume != 100 {
		t.Errorf("bad in-progress bar: %+v", wip)
	}

	// inProgress must not mutate state: calling it twice should be idempotent,
	// and a subsequent tick should still fold into the same open bucket.
	if wip2, _ := ba.inProgress("US.TST"); wip2 != wip {
		t.Errorf("inProgress not idempotent: %+v vs %+v", wip, wip2)
	}
	tk2 := feed.Tick{Symbol: "US.TST", TsMs: 60_500, Price: 10.50, Volume: 10, Turnover: 105}
	if closed := ba.add(tk2); closed != nil {
		t.Fatalf("same-bucket tick should not close a bar, got %+v", *closed)
	}
	wip, _ = ba.inProgress("US.TST")
	if wip.H != 10.50 || wip.C != 10.50 || wip.Volume != 110 {
		t.Errorf("in-progress bar did not accumulate second tick: %+v", wip)
	}
}
