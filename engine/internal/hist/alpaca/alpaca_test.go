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
	if gotPath != "/v2/stocks/AAPL/bars" || gotTF != "1Min" || gotAdj != "all" || gotFeed != "iex" {
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
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
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
