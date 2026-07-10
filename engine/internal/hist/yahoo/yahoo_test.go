package yahoo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// oneDayResp is a minimal v8 chart body: 2016-01-04 (a Monday). timestamp is
// the session-open instant (14:30Z summer / here 14:30Z is fine for the test);
// adjclose < close so the scaling factor is observable.
const oneDayResp = `{"chart":{"result":[{"timestamp":[1451919600],` +
	`"indicators":{"quote":[{"open":[10],"high":[11],"low":[9],"close":[10],"volume":[1000]}],` +
	`"adjclose":[{"adjclose":[5]}]}}],"error":null}}`

func TestDailyBarsScalesAndBucketsToETMidnight(t *testing.T) {
	var gotPath, gotInterval, gotRange, gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInterval = r.URL.Query().Get("interval")
		gotRange = r.URL.Query().Get("range")
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(oneDayResp))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v8/finance/chart/AAPL" || gotInterval != "1d" || gotRange != "" {
		t.Fatalf("request = path %q interval %q range %q (range must be empty — never use range=)", gotPath, gotInterval, gotRange)
	}
	if !strings.Contains(strings.ToLower(gotUA), "mozilla") {
		t.Fatalf("User-Agent = %q, want a browser UA", gotUA)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1", len(bars))
	}
	b := bars[0]
	// adjclose/close = 5/10 = 0.5 scaling on every OHLC; volume unscaled.
	if b.Symbol != "US.AAPL" || b.O != 5 || b.H != 5.5 || b.L != 4.5 || b.C != 5 || b.Volume != 1000 {
		t.Fatalf("scaled bar = %+v", b)
	}
	// Bucket normalized to ET-midnight of 2016-01-04.
	wantMid := time.Date(2016, 1, 4, 0, 0, 0, 0, session.Loc()).UnixMilli()
	if b.BucketMs != wantMid {
		t.Fatalf("BucketMs = %d, want ET-midnight %d", b.BucketMs, wantMid)
	}
}

func TestDailyBarsSkipsNullRows(t *testing.T) {
	body := `{"chart":{"result":[{"timestamp":[1451919600,1452006000],` +
		`"indicators":{"quote":[{"open":[10,null],"high":[11,null],"low":[9,null],"close":[10,null],"volume":[1000,null]}],` +
		`"adjclose":[{"adjclose":[10,null]}]}}],"error":null}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1 (the all-null second row is skipped)", len(bars))
	}
}

func TestDailyBarsMapsShareClassSymbol(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/BRK-B", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(oneDayResp))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.BRK.B", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v8/finance/chart/BRK-B" {
		t.Fatalf("path = %q, want /v8/finance/chart/BRK-B (dots→dashes)", gotPath)
	}
	if len(bars) != 1 || bars[0].Symbol != "US.BRK.B" {
		t.Fatalf("returned symbol = %+v, want US.BRK.B preserved", bars)
	}
}

func TestDailyBars429RetriesThenErrors(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// System clock so the single backoff sleep actually elapses (~500ms).
	c := New(srv.URL, clock.System{})
	_, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want a 429 error after retry", err)
	}
	if calls != 2 {
		t.Fatalf("http calls = %d, want 2 (one retry then give up)", calls)
	}
}

func TestIntraday1mUnsupported(t *testing.T) {
	c := New("", clock.NewFake(time.UnixMilli(0)))
	if _, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1)); err != ErrIntradayUnsupported {
		t.Fatalf("Intraday1m err = %v, want ErrIntradayUnsupported", err)
	}
}
