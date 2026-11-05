package screener

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fakeSource struct {
	discover func(context.Context) ([]Instrument, int, string, error)
	daily    func(context.Context, Instrument) ([]feed.Bar, error)
}

func (s fakeSource) Discover(ctx context.Context, _ wsmsg.ScreenerFilters, _, _ time.Time) ([]Instrument, int, string, error) {
	return s.discover(ctx)
}
func (s fakeSource) Daily(ctx context.Context, stock Instrument, _ time.Time) ([]feed.Bar, error) {
	return s.daily(ctx, stock)
}

type statePublisher chan wsmsg.ScreenerState

func (p statePublisher) Publish(_ wsmsg.Topic, _ string, value any) { p <- value.(wsmsg.ScreenerState) }
func nextStatus(t *testing.T, p statePublisher, status string) wsmsg.ScreenerState {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case state := <-p:
			if state.Status == status {
				return state
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", status)
			return wsmsg.ScreenerState{}
		}
	}
}

func TestServiceStableResultsCachingAndUnavailable(t *testing.T) {
	date := testDate("2026-11-04")
	stock, bars := history(date, 30)
	missing := stock
	missing.Symbol = "US.BAD"
	var calls atomic.Int32
	source := fakeSource{discover: func(context.Context) ([]Instrument, int, string, error) {
		return []Instrument{stock, missing}, 100, "", nil
	}, daily: func(_ context.Context, s Instrument) ([]feed.Bar, error) {
		calls.Add(1)
		if s.Symbol == missing.Symbol {
			return nil, errors.New("provider down")
		}
		return bars, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pub := make(statePublisher, 100)
	s := New(ctx, clock.NewFake(date.Add(17*time.Hour)), source, pub)
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	first := nextStatus(t, pub, "complete")
	s.wg.Wait()
	if len(first.Rows) != 1 || first.Unavailable != 1 || first.Checked != 2 || first.Universe != 100 || first.AsOf != "2026-11-04" {
		t.Fatalf("%+v", first)
	}
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	second := nextStatus(t, pub, "complete")
	s.wg.Wait()
	if second.RunID <= first.RunID || calls.Load() != 3 || first.Rows[0].Close != 97 {
		t.Fatalf("cache/snapshot: first=%+v second=%+v calls=%d", first, second, calls.Load())
	}
}

func TestServiceCancellationAndShutdown(t *testing.T) {
	entered := make(chan struct{}, 1)
	source := fakeSource{discover: func(ctx context.Context) ([]Instrument, int, string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, 0, "", ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	pub := make(statePublisher, 100)
	s := New(ctx, clock.NewFake(testDate("2026-11-04").Add(17*time.Hour)), source, pub)
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	<-entered
	s.Cancel("main/one")
	nextStatus(t, pub, "canceled")
	s.wg.Wait()
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/two", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	s.wg.Wait()
	nextStatus(t, pub, "canceled")
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/three", Filters: testFilters()}); err == nil {
		t.Fatal("accepted work after shutdown")
	}
}

func TestSupersededRunCannotPublishOldResults(t *testing.T) {
	date := testDate("2026-11-04")
	stock, bars := history(date, 30)
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	source := fakeSource{discover: func(context.Context) ([]Instrument, int, string, error) { return []Instrument{stock}, 1, "", nil }, daily: func(ctx context.Context, _ Instrument) ([]feed.Bar, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return bars, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pub := make(statePublisher, 100)
	s := New(ctx, clock.NewFake(date.Add(17*time.Hour)), source, pub)
	args := wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: testFilters()}
	if err := s.Start(args); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.Start(args); err != nil {
		t.Fatal(err)
	}
	state := nextStatus(t, pub, "complete")
	s.wg.Wait()
	if state.RunID != 2 || len(state.Rows) != 1 {
		t.Fatalf("%+v", state)
	}
	for len(pub) > 0 {
		if state := <-pub; state.RunID < 2 {
			t.Fatalf("stale publication %+v", state)
		}
	}
}

func TestStopCancelsQueuedWorkAndClosesAdmissions(t *testing.T) {
	entered := make(chan struct{}, 1)
	source := fakeSource{discover: func(ctx context.Context) ([]Instrument, int, string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, 0, "", ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, clock.NewFake(testDate("2026-11-04").Add(17*time.Hour)), source, make(statePublisher, 100))
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/two", Filters: testFilters()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not drain canceled work")
	}
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/three", Filters: testFilters()}); err == nil {
		t.Fatal("accepted work after Stop")
	}
}
