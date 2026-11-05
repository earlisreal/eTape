package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/hist/alpaca"
	"github.com/earlisreal/eTape/engine/internal/hist/yahoo"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fakeSource struct {
	discover func(context.Context) ([]Instrument, int, string, error)
	daily    func(context.Context, Instrument) ([]feed.Bar, error)
}

func (s fakeSource) Discover(ctx context.Context, _ wsmsg.ScreenerFilters, _, _ time.Time) (Discovery, error) {
	stocks, total, warning, err := s.discover(ctx)
	return Discovery{Stocks: stocks, Universe: total, Warning: warning}, err
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

func TestScreenCompletesWithoutOpenD(t *testing.T) {
	for _, test := range []struct {
		name      string
		useAlpaca bool
		missing   bool
		wantClose float64
	}{
		{name: "Yahoo only", wantClose: 103},
		{name: "Alpaca SIP covers the lookback", useAlpaca: true, wantClose: 105},
		{name: "Yahoo replaces incomplete Alpaca history", useAlpaca: true, missing: true, wantClose: 103},
	} {
		t.Run(test.name, func(t *testing.T) {
			testOfflineScreen(t, test.useAlpaca, test.missing, test.wantClose)
		})
	}
}

func testOfflineScreen(t *testing.T, useAlpaca, missing bool, wantClose float64) {
	t.Helper()
	date := testDate("2026-11-04")
	previous := date.AddDate(0, 0, -1)
	var yahooDailyCalls, alpacaCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/fc.yahoo.com", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "test"})
	})
	mux.HandleFunc("/v1/test/getcrumb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "test-crumb")
	})
	mux.HandleFunc("POST /v1/finance/screener", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("crumb") != "test-crumb" || r.Header.Get("Cookie") != "A1=test" {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}
		var query struct{ Offset int }
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			http.Error(w, "invalid discovery query", http.StatusBadRequest)
			return
		}
		if query.Offset == 0 {
			_, _ = fmt.Fprint(w, `{"finance":{"result":[{"start":0,"total":2,"count":1,"quotes":[{"symbol":"SPY","quoteType":"ETF","exchange":"NYQ"}]}]}}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"finance":{"result":[{"start":1,"total":2,"count":1,"quotes":[{"symbol":"TEST-A","shortName":"Test Stock","quoteType":"EQUITY","exchange":"NMS","firstTradeDateMilliseconds":%d,"marketCap":1030000000,"regularMarketPrice":103}]}]}}`, previous.Add(10*time.Hour).UnixMilli())
	})
	mux.HandleFunc("/v8/finance/chart/TEST-A", func(w http.ResponseWriter, _ *http.Request) {
		yahooDailyCalls.Add(1)
		_, _ = fmt.Fprintf(w, `{"chart":{"result":[{"timestamp":[%d,%d],"indicators":{"quote":[{"open":[95,100],"high":[100,104],"low":[90,99],"close":[97,103],"volume":[1000000,1000000]}]}}]}}`, previous.Add(10*time.Hour).Unix(), date.Add(10*time.Hour).Unix())
	})
	mux.HandleFunc("/v2/stocks/bars", func(w http.ResponseWriter, r *http.Request) {
		alpacaCalls.Add(1)
		if q := r.URL.Query(); q.Get("symbols") != "TEST.A" || q.Get("start") != previous.UTC().Format(time.RFC3339) {
			t.Errorf("unexpected Alpaca query %v", q)
		}
		if missing {
			_, _ = fmt.Fprintf(w, `{"bars":{"TEST.A":[{"t":%q,"o":95,"h":100,"l":90,"c":97,"v":1000000}]},"next_page_token":null}`, previous.UTC().Format(time.RFC3339))
			return
		}
		_, _ = fmt.Fprintf(w, `{"bars":{"TEST.A":[{"t":%q,"o":95,"h":100,"l":90,"c":97,"v":1000000},{"t":%q,"o":100,"h":106,"l":99,"c":105,"v":1000000}]},"next_page_token":null}`,
			previous.UTC().Format(time.RFC3339), date.UTC().Format(time.RFC3339))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	clk := clock.NewFake(date.Add(17 * time.Hour))
	var sip *alpaca.Client
	if useAlpaca {
		sip = alpaca.New(server.URL, "key", "secret", "sip", clk)
	}
	source := NewMarketSource(opend.New(opend.Options{Clock: clk}), yahoo.New(server.URL, clk), sip)
	pub := make(statePublisher, 10)
	s := New(ctx, clk, source, pub)
	filters := testFilters()
	filters.Mode = "new"
	filters.MinMarketCap = 1e9
	if err := s.Start(wsmsg.RunScreenerArgs{PanelID: "main/one", Filters: filters}); err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	close(pub)
	var last wsmsg.ScreenerState
	for state := range pub {
		last = state
	}
	if last.Status != "complete" || !strings.Contains(last.Warning, "Yahoo") || last.Universe != 1 || last.Checked != 1 || len(last.Rows) != 1 {
		t.Fatalf("screen with OpenD disconnected: %+v", last)
	}
	if row := last.Rows[0]; row.Symbol != "US.TEST.A" || row.Close != wantClose || row.PreviousHigh != 100 || !row.CloseConfirmed {
		t.Fatalf("offline result: %+v", row)
	}
	if (alpacaCalls.Load() == 1) != useAlpaca || (yahooDailyCalls.Load() == 1) != (!useAlpaca || missing) {
		t.Fatalf("Alpaca daily calls=%d, Yahoo daily calls=%d", alpacaCalls.Load(), yahooDailyCalls.Load())
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
