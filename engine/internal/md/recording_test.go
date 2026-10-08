package md

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type evidenceSink struct {
	mu      sync.Mutex
	rows    []feed.Recording
	changed chan struct{}
}

func TestRecordingPPCBWickIncludesRangeReportAndExactClamp(t *testing.T) {
	sink := &evidenceSink{changed: make(chan struct{}, 1)}
	c := New(Config{Recorder: sink})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	prices := []float64{4.01, 3.60, 3.9501, 3.90, 3.85}
	offsets := []int64{10000, 15000, 19000, 21000, 61000}
	var ticks []feed.Tick
	for i, price := range prices {
		r := tick(int64(i+1), offsets[i], price, 26, feed.Neutral)
		r.Symbol = "US.PPCB"
		r.Source = &feed.SourceRef{Run: "fixture", Connection: 2, Ingress: uint64(i + 1), Index: 0}
		ticks = append(ticks, r)
	}
	c.Feed(feed.TicksEvent{Ticks: ticks})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.PPCB", BucketMs: t0Ms, O: 4.0901, H: 4.13, L: 3.82, C: 3.85, Volume: 326, Source: feed.SourceRef{Run: "fixture", Connection: 2, Ingress: 10}}}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.PPCB", BucketMs: t0Ms + 60000, O: 3.85, H: 3.85, L: 3.85, C: 3.85, Volume: 1}}})
	c.SeedChartHistory("US.PPCB", nil, nil, nil)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var originalFound, clampFound bool
	for _, row := range sink.rows {
		if row.Kind == "bucket_basis" && row.Timeframe == string(session.TF10s) {
			basis, ok := row.Data.(bucketBasis)
			originalFound = originalFound || ok && basis.HasRange && basis.Low.Price == 3.60 && basis.Low.Source.Ingress == 2
		}
		if row.Kind == "clamp" {
			data, err := json.Marshal(row.Data)
			if err != nil {
				t.Fatal(err)
			}
			var correction struct {
				Before, After, Authoritative Bar
				SourceReferenceProvided      bool
			}
			if err := json.Unmarshal(data, &correction); err != nil {
				t.Fatal(err)
			}
			clampFound = clampFound || correction.Before.L == 3.60 && correction.After.L == 3.82 && correction.Authoritative.BucketMs == t0Ms && row.Source.Ingress == 10 && correction.SourceReferenceProvided
		}
	}
	if !originalFound || !clampFound {
		t.Fatalf("wick evidence original=%v exact correction=%v", originalFound, clampFound)
	}
}

func (*evidenceSink) RunID() string { return "fixture" }
func (s *evidenceSink) Record(r feed.Recording) bool {
	s.mu.Lock()
	s.rows = append(s.rows, r)
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return true
}
func (*evidenceSink) Lost(feed.RecordingLane, feed.SourceRef, uint64) {}

func TestRecordingDistinguishesDedupRejectionAndIndependentBucketLateness(t *testing.T) {
	sink := &evidenceSink{changed: make(chan struct{}, 1)}
	c := New(Config{Recorder: sink})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(1, 0, 2.37, 10, feed.Buy), tick(1, 0, 2.37, 10, feed.Buy), tick(2, 11000, 2.39, 10, feed.Buy), tick(3, 5000, 1.9, 5, feed.Sell)}})
	deadline := time.After(2 * time.Second)
	for {
		sink.mu.Lock()
		var records []feed.Recording
		for _, r := range sink.rows {
			if r.Kind == "processing" {
				records = append(records, r)
			}
		}
		sink.mu.Unlock()
		if len(records) == 4 {
			foundRejected, foundLate := false, false
			for _, r := range records {
				data, err := json.Marshal(r.Data)
				if err != nil {
					t.Fatal(err)
				}
				var decision struct{ Accepted, EligibilityStamped, TenLate, ShadowLate bool }
				if err := json.Unmarshal(data, &decision); err != nil {
					t.Fatal(err)
				}
				if !decision.Accepted {
					foundRejected = true
					if decision.EligibilityStamped {
						t.Fatal("rejected report was classified")
					}
				}
				if r.Sequence == 3 {
					foundLate = decision.Accepted && decision.TenLate && !decision.ShadowLate
				}
			}
			if !foundRejected || !foundLate {
				t.Fatalf("recording lost decisions: %+v", records)
			}
			return
		}
		select {
		case <-sink.changed:
		case <-deadline:
			t.Fatalf("processing trace count = %d", len(records))
		}
	}
}

func TestHistoryAnchorRetainsFullMinuteWithoutRawSource(t *testing.T) {
	sink := &evidenceSink{changed: make(chan struct{}, 1)}
	c := New(Config{Recorder: sink})
	raw := feed.Bar{Symbol: "US.AIXI", BucketMs: t0Ms, O: 2.01, H: 2.67, L: 1.90, C: 2.37, Volume: 1234}
	c.bars.seedHistory1m(c, raw.Symbol, []feed.Bar{raw})
	a := c.bars.sym(raw.Symbol).agg10
	r := tick(1, 70000, 2.39, 10, feed.Neutral)
	r.Symbol = raw.Symbol
	r.RangeEligible, r.LastEligible, r.VolumeEligible = true, true, true
	a.addTick(r, false)
	r.TsMs += 10000
	a.addTick(r, false)
	later := a.open[session.BucketStartMs(r.TsMs, session.TF10s)]
	if later.anchorBar != nil || later.anchorOrigin != "last_eligible_report" {
		t.Fatalf("stale history evidence on report anchor: %+v", later)
	}
	for _, row := range sink.rows {
		if basis, ok := row.Data.(bucketBasis); ok && basis.AnchorBar != nil {
			if *basis.AnchorBar != raw || basis.AnchorOrigin != "engine_history_1m" {
				t.Fatalf("history evidence=%+v", basis)
			}
			return
		}
	}
	t.Fatal("full history minute missing from bucket basis")
}
