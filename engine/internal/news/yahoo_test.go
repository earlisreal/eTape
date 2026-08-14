package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestYahooFetcher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "AAPL" {
			t.Fatalf("query q = %q", got)
		}
		if got := r.URL.Query().Get("newsCount"); got != "2" {
			t.Fatalf("query newsCount = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "eTape/experimental-news" {
			t.Fatalf("user agent = %q", got)
		}
		_, _ = w.Write([]byte(`{"news":[{"title":"Apple story","publisher":"Example Wire","link":"https://example.test/aapl","providerPublishTime":1786707604,"relatedTickers":["AAPL","TSLA","^GSPC"]}]}`))
	}))
	defer server.Close()

	fetcher := yahooFetcher{client: server.Client(), endpoint: server.URL, maxCount: 2}
	items, err := fetcher.fetch(context.Background(), "US.AAPL")
	if err != nil || len(items) != 1 {
		t.Fatalf("fetch = %+v, %v", items, err)
	}
	item := items[0]
	if item.Source != "Yahoo Finance / Example Wire" || item.URL != "https://example.test/aapl" {
		t.Fatalf("metadata = %+v", item)
	}
	if want := time.Unix(1786707604, 0).In(session.Loc()).Format("2006-01-02 15:04:05"); item.PublishTime != want {
		t.Fatalf("published = %q, want %q", item.PublishTime, want)
	}
	if want := []string{"US.AAPL", "US.TSLA"}; !reflect.DeepEqual(item.RelatedSecurities, want) {
		t.Fatalf("related = %v, want %v", item.RelatedSecurities, want)
	}
}

func TestYahooFetcherSkipsNonUSSymbol(t *testing.T) {
	fetcher := yahooFetcher{client: http.DefaultClient, endpoint: "https://example.test", maxCount: 1}
	items, err := fetcher.fetch(context.Background(), "HK.00700")
	if err != nil || items != nil {
		t.Fatalf("non-US fetch = %+v, %v", items, err)
	}
}
