package mexc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestPublicDemandSharesSubscriptionAndRecoversBook(t *testing.T) {
	const native = "BTC_USDT"
	const symbol = "PERP.MEXC." + native
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/contract/detail/country", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("symbol") != native {
			t.Error("unexpected catalog symbol", r.URL.Query())
		}
		fmt.Fprint(w, `{"success":true,"data":{"symbol":"BTC_USDT","futureType":1,"quoteCoin":"USDT","settleCoin":"USDT","contractSize":0.0001,"volUnit":1,"priceUnit":0.1}}`)
	})
	mux.HandleFunc("/api/v1/contract/kline/"+native, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"time":[1795041480],"open":[62598.9],"high":[62598.9],"low":[62587.9],"close":[62587.9],"vol":[18523],"amount":[115934.92648]}}`)
	})
	mux.HandleFunc("/api/v1/contract/depth/"+native, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"version":42,"timestamp":1795041491205,"bids":[[62588.5,12,1]],"asks":[[62589,5,1]]}}`)
	})
	mux.HandleFunc("/api/v1/contract/depth_commits/"+native+"/1000", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":[{"version":44,"timestamp":1795041491205,"asks":[[62589,11,1]]},{"version":43,"timestamp":1795041491205,"bids":[[62588.5,0,0]]}]}`)
	})
	received := make(chan request, 8)
	mux.HandleFunc("/edge", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			var sub request
			if wsjson.Read(r.Context(), conn, &sub) != nil {
				return
			}
			received <- sub
			frame := message{Symbol: native}
			switch sub.Method {
			case "sub.deal":
				frame.Channel = "push.deal"
				frame.Data = json.RawMessage(`[{"p":62587.9,"v":5,"T":2,"t":1795041491205,"i":"17076571996"}]`)
			case "sub.depth":
				frame.Channel = "push.depth"
				frame.Data = json.RawMessage(`{"version":45,"begin":45,"end":45,"cts":"1795041491206","bids":[[62588.4,9,1]],"asks":[]}`)
			default:
				continue
			}
			if wsjson.Write(r.Context(), conn, frame) != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := NewClient()
	client.base, client.stream = server.URL, "ws"+strings.TrimPrefix(server.URL, "http")+"/edge"
	c, err := client.Contract(ctx, symbol)
	if err != nil || c.Multiplier.String() != "0.0001" || c.PriceTick.String() != "0.1" {
		t.Fatalf("contract %+v, %v", c, err)
	}
	bars, err := client.History(ctx, symbol, feed.Res1m)
	if err != nil || len(bars) != 1 || bars[0].Volume != 18523 || bars[0].BucketMs != 1795041480000 {
		t.Fatalf("bars %+v, %v", bars, err)
	}
	stream := NewStream(client)
	stream.Ensure(feed.Demand{ID: "tape-a", Symbol: symbol, Subs: []feed.SubType{feed.SubTicker}})
	stream.Ensure(feed.Demand{ID: "tape-b", Symbol: symbol, Subs: []feed.SubType{feed.SubTicker}})
	stream.Ensure(feed.Demand{ID: "book", Symbol: symbol, Subs: []feed.SubType{feed.SubBook}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		stream.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	var sawTick, sawBook bool
	for !sawTick || !sawBook {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for market data")
		case next := <-stream.Events():
			switch event := next.(type) {
			case feed.TicksEvent:
				tick := event.Ticks[0]
				if tick.Symbol != symbol || tick.Seq != 17076571996 || tick.Volume != 5 || tick.Dir != feed.Sell {
					t.Fatalf("lost trade identity/size: %+v", tick)
				}
				sawTick = true
			case feed.BookEvent:
				b := event.Book
				if len(b.Bids) == 0 {
					continue
				}
				if b.Symbol != symbol || b.Bids[0].Price != 62588.4 || b.Bids[0].Volume != 9 || b.Asks[0].Volume != 11 {
					t.Fatalf("bad depth snapshot %+v", b)
				}
				sawBook = true
			}
		}
	}
	counts := make(map[string]int)
	for range 3 {
		select {
		case sub := <-received:
			counts[sub.Method]++
			if sub.Method == "sub.deal" && (sub.Compress == nil || *sub.Compress) {
				t.Fatal("individual trade subscription used aggregation")
			}
		case <-ctx.Done():
			t.Fatal("expected three shared subscriptions")
		}
	}
	if counts["sub.deal"] != 1 || counts["sub.depth"] != 1 || counts["sub.ticker"] != 1 {
		t.Fatalf("unexpected subscription counts %v", counts)
	}
}
