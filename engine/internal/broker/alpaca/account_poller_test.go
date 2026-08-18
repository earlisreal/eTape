package alpaca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

const pollAccountJSON = `{"equity":"100050","last_equity":"100000","buying_power":"200000","cash":"50000","multiplier":"4"}`

func newPollerAdapter(t *testing.T, venue exec.VenueID, base string, clk clock.Clock) *Adapter {
	t.Helper()
	a, err := New(Config{Venue: venue, RESTBase: base, WSURL: "ws://unused", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func eventuallyPoller(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func startAccountPoller(t *testing.T, p *AccountPoller) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	return cancel, done
}

func stopAccountPoller(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("account poller did not stop")
	}
}

func waitPollerChange(t *testing.T, p *AccountPoller) {
	t.Helper()
	select {
	case <-p.Changes():
	case <-time.After(5 * time.Second):
		t.Fatal("account poller health did not change")
	}
}

func TestAccountPoller_ImmediateThenOneHz(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer srv.Close()
	a := newPollerAdapter(t, "alpaca-paper", srv.URL, clk)
	p := NewAccountPoller(map[exec.VenueID]*Adapter{"alpaca-paper": a}, "alpaca-paper", clk)
	cancel, done := startAccountPoller(t, p)
	defer stopAccountPoller(t, cancel, done)

	eventuallyPoller(t, func() bool { return requests.Load() == 1 })
	clk.Advance(999 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests after 999ms = %d, want 1", got)
	}
	clk.Advance(time.Millisecond)
	eventuallyPoller(t, func() bool { return requests.Load() == 2 })
	if rtt, ok, active := p.Latest(); !ok || !active || rtt < 0 {
		t.Fatalf("account health = rtt=%v ok=%v active=%v", rtt, ok, active)
	}
	select {
	case event := <-a.Events():
		account, ok := event.(exec.BrokerAccount)
		if !ok || account.Account.DayPnL != 50 || account.Account.Venue != "alpaca-paper" {
			t.Fatalf("poll account event = %T %+v", event, event)
		}
	default:
		t.Fatal("successful poll did not emit BrokerAccount")
	}
}

func TestAccountPoller_NonAlpacaActiveDoesNothing(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer srv.Close()
	a := newPollerAdapter(t, "alpaca-paper", srv.URL, clk)
	p := NewAccountPoller(map[exec.VenueID]*Adapter{"alpaca-paper": a}, "sim-paper", clk)
	cancel, done := startAccountPoller(t, p)
	defer stopAccountPoller(t, cancel, done)

	clk.Advance(10 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if requests.Load() != 0 {
		t.Fatalf("inactive Alpaca received %d requests", requests.Load())
	}
	if _, ok, active := p.Latest(); ok || active {
		t.Fatalf("inactive Alpaca health = ok=%v active=%v", ok, active)
	}
}

func TestAccountPoller_SwitchStartsOnlyNewActiveVenue(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	var paper, live atomic.Int32
	paperSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		paper.Add(1)
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer paperSrv.Close()
	liveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		live.Add(1)
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer liveSrv.Close()
	paperAdapter := newPollerAdapter(t, "alpaca-paper", paperSrv.URL, clk)
	liveAdapter := newPollerAdapter(t, "alpaca-live", liveSrv.URL, clk)
	p := NewAccountPoller(map[exec.VenueID]*Adapter{
		"alpaca-paper": paperAdapter, "alpaca-live": liveAdapter,
	}, "alpaca-paper", clk)
	cancel, done := startAccountPoller(t, p)
	defer stopAccountPoller(t, cancel, done)
	eventuallyPoller(t, func() bool { return paper.Load() == 1 })

	p.SetActiveVenue("alpaca-live")
	eventuallyPoller(t, func() bool {
		_, ok, active := p.Latest()
		return live.Load() == 1 && ok && active
	})
	if paper.Load() != 1 {
		t.Fatalf("paper requests after switch = %d, want 1", paper.Load())
	}
	if _, ok, active := p.Latest(); !ok || !active {
		t.Fatalf("live health after switch = ok=%v active=%v", ok, active)
	}
}

func TestAccountPoller_FailureMarksDownAndRetriesWithoutAccountEvent(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer srv.Close()
	a := newPollerAdapter(t, "alpaca-paper", srv.URL, clk)
	p := NewAccountPoller(map[exec.VenueID]*Adapter{"alpaca-paper": a}, "alpaca-paper", clk)
	cancel, done := startAccountPoller(t, p)
	defer stopAccountPoller(t, cancel, done)

	eventuallyPoller(t, func() bool { return requests.Load() == 1 })
	waitPollerChange(t, p)
	if _, ok, active := p.Latest(); ok || !active {
		t.Fatalf("failed account health = ok=%v active=%v, want active/down", ok, active)
	}
	select {
	case event := <-a.Events():
		t.Fatalf("failed account emitted %T %+v", event, event)
	default:
	}
	clk.Advance(time.Second)
	waitPollerChange(t, p)
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests after failure retry = %d, want 2", got)
	}
	if _, ok, active := p.Latest(); !ok || !active {
		t.Fatalf("recovered account health = ok=%v active=%v", ok, active)
	}
}

func TestAccountPoller_SingleFlightAndCancellation(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	started := make(chan struct{})
	release := make(chan struct{})
	var current, maximum, requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		inFlight := current.Add(1)
		for {
			old := maximum.Load()
			if inFlight <= old || maximum.CompareAndSwap(old, inFlight) {
				break
			}
		}
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		current.Add(-1)
		_, _ = w.Write([]byte(pollAccountJSON))
	}))
	defer srv.Close()
	a := newPollerAdapter(t, "alpaca-paper", srv.URL, clk)
	p := NewAccountPoller(map[exec.VenueID]*Adapter{"alpaca-paper": a}, "alpaca-paper", clk)
	cancel, done := startAccountPoller(t, p)
	eventuallyPoller(t, func() bool { return requests.Load() == 1 })
	clk.Advance(5 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent account requests = %d, want 1", got)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests while first blocked = %d, want 1", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop after context cancellation")
	}
}
