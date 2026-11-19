package linearperp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestPerpetualCatalogsAndCandleHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixMilli()
		switch r.URL.Path {
		case "/fapi/v1/ticker/24hr":
			fmt.Fprintf(w, `[{"symbol":"BTCUSDT","lastPrice":"100","quoteVolume":"12345","closeTime":%d}]`, now)
		case "/fapi/v1/ticker/bookTicker":
			fmt.Fprintf(w, `[{"symbol":"BTCUSDT","bidPrice":"99.9","askPrice":"100.1","time":%d}]`, now)
		case "/openApi/swap/v2/quote/ticker":
			fmt.Fprintf(w, `{"code":0,"data":[{"symbol":"BTC-USDT","lastPrice":"100","quoteVolume":"12345","bidPrice":"99.9","askPrice":"100.1","closeTime":%d}]}`, now)
		case "/fapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[
				{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","status":"TRADING","underlyingType":"COIN",
				 "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.10","minPrice":"200.00","maxPrice":"800000"},
				            {"filterType":"LOT_SIZE","stepSize":"0.0001","minQty":"0.0001","maxQty":"1000"},
				            {"filterType":"MARKET_LOT_SIZE","stepSize":"0.0001","minQty":"0.0001","maxQty":"120"},
				            {"filterType":"MIN_NOTIONAL","notional":"50"}]},
				{"symbol":"TSLAUSDT","baseAsset":"TSLA","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","status":"TRADING","underlyingType":"EQUITY"},
				{"symbol":"ETHUSDC","baseAsset":"ETH","quoteAsset":"USDC","marginAsset":"USDC","contractType":"PERPETUAL","status":"TRADING","underlyingType":"COIN"}
			]}`)
		case "/fapi/v1/klines":
			if r.URL.Query().Get("symbol") != "BTCUSDT" {
				t.Error("Binance received another venue's symbol")
			}
			fmt.Fprint(w, `[[1795060500000,"63153.40","63168.80","63135.10","63159.30","1.001",1795060559999,"63191.874"]] `)
		case "/openApi/swap/v2/quote/contracts":
			fmt.Fprint(w, `{"code":0,"data":[{"symbol":"BTC-USDT","asset":"BTC","currency":"USDT","status":1},
				{"symbol":"NCSKTSLA2USD-USDT","asset":"NCSKTSLA2USD","currency":"USDT","status":1}]}`)
		case "/openApi/swap/v1/quote/tradingSchedule":
			fmt.Fprint(w, `{"code":0,"data":[{"symbol":"NCSKTSLA2USD-USDT"}]}`)
		case "/openApi/swap/v3/quote/klines":
			if r.URL.Query().Get("symbol") != "BTC-USDT" {
				t.Error("BingX received another venue's symbol")
			}
			fmt.Fprint(w, `{"code":0,"data":[{"time":1795060560000,"open":"63159","high":"63175","low":"63125","close":"63140","volume":"1.001"},
				{"time":1795060500000,"open":"63155","high":"63160","low":"63150","close":"63159","volume":"1.001"}]}`)
		default:
			t.Errorf("unexpected market URL %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, pair := range []struct {
		client  *Client
		native  string
		trading bool
	}{{NewBinance(), "BTCUSDT", false}, {NewBinanceDemo(), "BTCUSDT", true}, {NewBingX(), "BTC-USDT", false}} {
		c := pair.client
		ctx := context.Background()
		c.base = server.URL
		symbol := "PERP." + c.exchange + "." + pair.native
		rows, err := c.PerpetualMarkets(ctx)
		if err != nil || len(rows) != 1 || rows[0].ID.String() != symbol || rows[0].TradingEnabled != pair.trading {
			t.Fatalf("%s market list %+v, %v", c.exchange, rows, err)
		}
		if pair.trading {
			rules, ok := c.DemoRules(symbol)
			if !ok || rules.Multiplier != "0.0001" || rules.PriceTick != "0.10" ||
				rules.MinNotional != 50 || rules.Maximum != 10_000_000 || rules.Market.Maximum != 1_200_000 {
				t.Fatalf("incorrect Demo lot and price filters: %+v", rules)
			}
			if _, ok := c.DemoRules("PERP.BINANCE.BTCUSDT"); ok {
				t.Fatal("Demo order rules accepted a production symbol")
			}
		}
		if err := c.Validate(ctx, symbol); err != nil {
			t.Fatal(err)
		}
		scanner, err := c.CryptoScanner(ctx)
		if err != nil || len(scanner.Rows) != 1 || scanner.Rows[0].Symbol != symbol ||
			scanner.Rows[0].Turnover == nil || *scanner.Rows[0].Turnover != 12345 || scanner.Rows[0].SpreadBps == nil ||
			*scanner.Rows[0].SpreadBps < 19.99 || *scanner.Rows[0].SpreadBps > 20.01 {
			t.Fatalf("%s scanner %+v, %v", c.exchange, scanner, err)
		}
		bars, err := c.History(ctx, symbol, feed.Res1m)
		if err != nil || len(bars) == 0 || bars[0].BucketMs != 1795060500000 ||
			bars[0].Volume != 100100000 || bars[0].Symbol != symbol {
			t.Fatalf("%s bars %+v, %v", c.exchange, bars, err)
		}
	}
}

func TestPublicStreamsDecodeVenueUnits(t *testing.T) {
	for _, f := range []struct {
		exchange, symbol, tick, book, bar string
	}{{
		Binance, "PERP.BINANCE.BTCUSDT",
		`{"T":1795060621004,"a":3532299960,"p":"63138.40","q":"0.003","m":true}`,
		`{"e":"depthUpdate","E":1795060621267,"U":17141504,"u":17141505,"b":[["63130","7.257"]],"a":[["63132","0.001"],["63131","1.01"]]}`,
		`{"k":{"t":1795060620000,"T":1795060679999,"o":"63133","h":"63134","l":"63132","c":"63134","v":"0.040","q":"2525.53970"}}`,
	}, {
		BingX, "PERP.BINGX.BTC-USDT",
		`[{"T":1795060621004,"t":"3532299960","p":"63138.40","q":"0.003","m":true}]`,
		`{"bids":[["63130","7.257"]],"asks":[["63132","0.001"],["63131","1.01"]]}`,
		`[{"T":1795060620000,"o":"63133","h":"63134","l":"63132","c":"63134","v":"0.040"}]`,
	}} {
		t.Run(f.exchange, func(t *testing.T) {
			event, _ := parseEvent(f.exchange, topic{symbol: f.symbol, kind: tapeTopic}, json.RawMessage(f.tick), 0)
			ticks, ok := event.(feed.TicksEvent)
			if !ok || len(ticks.Ticks) != 1 || ticks.Ticks[0].Volume != 300000 || ticks.Ticks[0].Dir != feed.Sell ||
				ticks.Ticks[0].Seq != 3532299960 || ticks.Ticks[0].Symbol != f.symbol {
				t.Fatalf("bad tape %+v", event)
			}
			event, rev := parseEvent(f.exchange, topic{symbol: f.symbol, kind: bookTopic}, json.RawMessage(f.book), 1795060621267)
			book, ok := event.(feed.BookEvent)
			if !ok || rev <= 0 || len(book.Book.Bids) != 1 || len(book.Book.Asks) != 2 ||
				book.Book.Bids[0].Volume != 725700000 || book.Book.Asks[0].Price != 63131 {
				t.Fatalf("bad depth %+v revision %d", event, rev)
			}
			event, _ = parseEvent(f.exchange, topic{symbol: f.symbol, kind: barTopic}, json.RawMessage(f.bar), 0)
			candles, ok := event.(feed.Bars1mEvent)
			if !ok || len(candles.Bars) != 1 || candles.Bars[0].BucketMs != 1795060620000 || candles.Bars[0].Volume != 4000000 {
				t.Fatalf("bad candle %+v", event)
			}
			event, _ = parseEvent(f.exchange, topic{symbol: f.symbol, kind: markTopic},
				json.RawMessage(`{"e":"markPriceUpdate","E":1795060621267,"p":"63138.4","P":"63036.4","i":"63159.0","r":"0.0000204","T":1795075200000}`), 0)
			mark, ok := event.(feed.QuoteEvent)
			if !ok || mark.Quote.Mark != 63138.4 || mark.Quote.Index != 63159 || mark.Quote.NextFundingMs != 1795075200000 {
				t.Fatalf("bad mark %+v", event)
			}
		})
	}
}

func TestBingXConnectionSharesTapeDemand(t *testing.T) {
	received := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			var sub map[string]string
			if wsjson.Read(r.Context(), conn, &sub) != nil {
				return
			}
			received <- sub["reqType"] + ":" + sub["dataType"]
			if sub["dataType"] == "BTC-USDT@trade" && sub["reqType"] == "sub" {
				raw := []byte(`{"dataType":"BTC-USDT@trade","data":[{"t":"42","q":"0.001","p":"60000","T":1795060621004,"m":false}]}`)
				if conn.Write(r.Context(), websocket.MessageText, raw) != nil {
					return
				}
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	c := NewBingX()
	c.marketWS = "ws" + server.URL[len("http"):]
	stream := NewStream(c)
	const symbol = "PERP.BINGX.BTC-USDT"
	stream.Ensure(feed.Demand{ID: "a", Symbol: symbol, Subs: []feed.SubType{feed.SubTicker}})
	stream.Ensure(feed.Demand{ID: "b", Symbol: symbol, Subs: []feed.SubType{feed.SubTicker}})
	wg.Go(func() { stream.Run(ctx) })
	select {
	case ev := <-stream.Events():
		ticks, ok := ev.(feed.TicksEvent)
		if !ok || len(ticks.Ticks) != 1 || ticks.Ticks[0].Seq != 42 {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("ticker timed out")
	}
	stream.Release("a")
	stream.Release("b")
	counts := map[string]int{}
	for range 4 {
		select {
		case sub := <-received:
			counts[sub]++
		case <-ctx.Done():
			t.Fatal("subscription change timed out")
		}
	}
	if counts["sub:BTC-USDT@trade"] != 1 || counts["unsub:BTC-USDT@trade"] != 1 ||
		counts["sub:BTC-USDT@markPrice"] != 1 || counts["unsub:BTC-USDT@markPrice"] != 1 {
		t.Fatalf("mismatched subscriptions %+v", counts)
	}
}
