package stockinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestYahooMetadataFetchAndCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/fc.yahoo.com" {
			w.Header().Add("Set-Cookie", "A1=test-cookie; Path=/")
			return
		}
		if r.URL.Path == "/v1/test/getcrumb" {
			if r.Header.Get("Cookie") != "A1=test-cookie" {
				t.Fatalf("crumb cookie = %q", r.Header.Get("Cookie"))
			}
			_, _ = w.Write([]byte("test-crumb"))
			return
		}
		if r.URL.Path != "/v10/finance/quoteSummary/BRK-B" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("modules") != "assetProfile" {
			t.Fatalf("modules = %q", r.URL.Query().Get("modules"))
		}
		if r.URL.Query().Get("crumb") != "test-crumb" {
			t.Fatalf("crumb = %q", r.URL.Query().Get("crumb"))
		}
		if r.Header.Get("Cookie") != "A1=test-cookie" {
			t.Fatalf("profile cookie = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("User-Agent") == "" {
			t.Fatal("missing User-Agent")
		}
		_, _ = w.Write([]byte(`{"quoteSummary":{"result":[{"assetProfile":{"country":"United States","sector":"Technology","industry":"Consumer Electronics"}}],"error":null}}`))
	}))
	defer server.Close()

	cache := newYahooMetadataCache(server.URL, clock.System{}, server.Client())
	cache.refresh(context.Background(), "US.BRK.B")
	got := cache.get(context.Background(), "US.BRK.B")
	if got != (yahooMetadata{Country: "United States", Sector: "Technology", Industry: "Consumer Electronics"}) {
		t.Fatalf("metadata = %+v", got)
	}
	if calls != 3 {
		t.Fatalf("cached metadata made %d requests, want session + profile (3)", calls)
	}
}

func TestApplyYahooMetadataOnlyFallsBackForBlankIndustry(t *testing.T) {
	value := yahooMetadata{Country: "United States", Sector: "Technology", Industry: "Consumer Electronics"}
	withMoomoo := wsmsg.StockDetailPayload{Industry: "Moomoo Industry"}
	applyYahooMetadata(&withMoomoo, value)
	if withMoomoo.Country != value.Country || withMoomoo.Sector != value.Sector || withMoomoo.Industry != "Moomoo Industry" {
		t.Fatalf("Moomoo precedence lost: %+v", withMoomoo)
	}

	withoutMoomoo := wsmsg.StockDetailPayload{}
	applyYahooMetadata(&withoutMoomoo, value)
	if withoutMoomoo.Industry != value.Industry {
		t.Fatalf("Yahoo fallback industry = %q, want %q", withoutMoomoo.Industry, value.Industry)
	}
}
