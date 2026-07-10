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

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
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

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
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

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
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

	c := New(srv.URL, "K", "S", "", clock.NewFake(time.UnixMilli(0))) // empty feed => default
	if _, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40)); err != nil {
		t.Fatal(err)
	}
	if gotFeed != "sip" {
		t.Fatalf("default feed = %q, want sip", gotFeed)
	}
}

func TestIntraday1mRetriesOnRecentSIP403(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	var calls int
	var secondEnd string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"subscription does not permit querying recent SIP data"}`))
			return
		}
		secondEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-08T15:00:00Z","o":1,"h":1,"l":1,"c":1,"v":1}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "sip", clock.NewFake(now))
	bars, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("err = %v, want the clamped retry to succeed", err)
	}
	if calls != 2 || len(bars) != 1 {
		t.Fatalf("calls = %d bars = %d, want 2 and 1", calls, len(bars))
	}
	// Retry clamps end to now−16m = 2026-07-08T11:44:00Z.
	wantEnd := now.Add(-16 * time.Minute).UTC().Format(time.RFC3339)
	if secondEnd != wantEnd {
		t.Fatalf("retry end = %q, want %q", secondEnd, wantEnd)
	}
}
