package alpaca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestIntraday1mParsesStripsPrefixAndMapsTime(t *testing.T) {
	var gotPath, gotTF, gotAdj, gotFeed string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTF = r.URL.Query().Get("timeframe")
		gotAdj = r.URL.Query().Get("adjustment")
		gotFeed = r.URL.Query().Get("feed")
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:30:00Z","o":100,"h":101,"l":99.5,"c":100.5,"v":1234}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(1<<40)))
	bars, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err != nil {
		t.Fatal(err)
	}
	// Intraday requests unadjusted bars so they match the raw scale of the live
	// tick/quote feed — daily alone stays "all"-adjusted (see bars()'s comment).
	if gotPath != "/v2/stocks/AAPL/bars" || gotTF != "1Min" || gotAdj != "raw" || gotFeed != "iex" {
		t.Fatalf("request = path %q tf %q adj %q feed %q", gotPath, gotTF, gotAdj, gotFeed)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1", len(bars))
	}
	b := bars[0]
	// Symbol keeps the US. prefix; time maps to epoch-ms bucket start.
	if b.Symbol != "US.AAPL" || b.BucketMs != 1783431000_000 || b.O != 100 || b.C != 100.5 || b.Volume != 1234 {
		t.Fatalf("bar = %+v", b)
	}
}

func TestIntraday1mCapsEndAtNowMinus16m(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	var gotEnd string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	_, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), now.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	wantEnd := now.Add(-16 * time.Minute).UTC().Format(time.RFC3339)
	if gotEnd != wantEnd {
		t.Fatalf("end = %q, want clamp %q", gotEnd, wantEnd)
	}
}

func TestIntraday1mPreservesOlderEnd(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	wantEnd := now.Add(-time.Hour)
	var gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	if _, err := c.Intraday1m(context.Background(), "US.AAPL", wantEnd.Add(-time.Hour), wantEnd); err != nil {
		t.Fatal(err)
	}
	if gotEnd != wantEnd.Format(time.RFC3339) {
		t.Fatalf("end = %q, want %q", gotEnd, wantEnd.Format(time.RFC3339))
	}
}

func TestIntraday1mSkipsEmptyCappedRange(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	bars, err := c.Intraday1m(context.Background(), "US.AAPL", now.Add(-16*time.Minute), now)
	if err != nil || len(bars) != 0 || requests != 0 {
		t.Fatalf("bars = %v, err = %v, requests = %d; want empty, nil, 0", bars, err, requests)
	}
}

func TestDailyBarsCapsEndAtNowMinus24h(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	var gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	if _, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantEnd := now.Add(-24 * time.Hour).Format(time.RFC3339)
	if gotEnd != wantEnd {
		t.Fatalf("end = %q, want clamp %q", gotEnd, wantEnd)
	}
}

func TestDailyBarsPreservesOlderEnd(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	wantEnd := now.Add(-48 * time.Hour)
	var gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	if _, err := c.DailyBars(context.Background(), "US.AAPL", wantEnd.Add(-24*time.Hour), wantEnd); err != nil {
		t.Fatal(err)
	}
	if gotEnd != wantEnd.Format(time.RFC3339) {
		t.Fatalf("end = %q, want %q", gotEnd, wantEnd.Format(time.RFC3339))
	}
}

func TestDailyBarsSkipsEmptyCappedRange(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(now))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", now.Add(-24*time.Hour), now)
	if err != nil || len(bars) != 0 || requests != 0 {
		t.Fatalf("bars = %v, err = %v, requests = %d; want empty, nil, 0", bars, err, requests)
	}
}

func TestBarsPaginateViaNextPageToken(t *testing.T) {
	var gotAdj string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotAdj = r.URL.Query().Get("adjustment")
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:30:00Z","o":1,"h":1,"l":1,"c":1,"v":1}],"next_page_token":"PAGE2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:31:00Z","o":2,"h":2,"l":2,"c":2,"v":2}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(1<<40)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err != nil {
		t.Fatal(err)
	}
	// Daily stays adjustment=all (split + dividend) — only Intraday1m drops it,
	// see TestIntraday1mParsesStripsPrefixAndMapsTime.
	if gotAdj != "all" {
		t.Fatalf("DailyBars adjustment = %q, want all", gotAdj)
	}
	if len(bars) != 2 || bars[0].C != 1 || bars[1].C != 2 {
		t.Fatalf("paginated bars = %+v", bars)
	}
}

func TestBarsErrorStatusSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(1<<40)))
	_, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a 403 error, got %v", err)
	}
}

func TestNewDefaultsToSIP(t *testing.T) {
	var gotFeed string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotFeed = r.URL.Query().Get("feed")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "", clock.NewFake(time.UnixMilli(1<<40))) // empty feed => default
	if _, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40)); err != nil {
		t.Fatal(err)
	}
	if gotFeed != "sip" {
		t.Fatalf("default feed = %q, want sip", gotFeed)
	}
}
