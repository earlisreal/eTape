package alpaca

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStockShortCapacity(t *testing.T) {
	for _, test := range []struct {
		name, borrow string
		enabled      bool
		want         float64
	}{
		{name: "easy to borrow", borrow: "easy_to_borrow", enabled: true, want: 100},
		{name: "hard to borrow", borrow: "hard_to_borrow", enabled: true},
		{name: "shorting disabled", borrow: "easy_to_borrow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v2/assets/AAPL", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"symbol":"AAPL","tradable":true,"shortable":true,"borrow_status":%q}`, test.borrow)
			})
			mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"buying_power":"1031","shorting_enabled":%t}`, test.enabled)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			adapter, err := New(Config{Venue: "paper", RESTBase: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			qty, err := adapter.StockShortCapacity(t.Context(), "US.AAPL", 10.30)
			if qty != test.want || (err != nil) != (test.want == 0) {
				t.Fatalf("capacity = %g, err = %v", qty, err)
			}
		})
	}
}
