package bingx

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestPrivateFillStreamUsesFillHistory(t *testing.T) {
	for _, env := range []environment{virtual, live} {
		t.Run(env.label, func(t *testing.T) {
			const clientID = "ET01ABCD12345678901111111111"
			const brokerID = "90071992547409939"
			now := time.Now()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var deleted atomic.Bool
			var orderReads, fillReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case listenKeyPath:
					if r.Header.Get("X-BX-APIKEY") != "key" || r.URL.Query().Get("signature") == "" {
						t.Error("listen-key request was not signed")
					}
					if r.Method == http.MethodPost {
						fmt.Fprint(w, `{"listenKey":"fixture-key"}`)
					} else if r.Method == http.MethodDelete && r.URL.Query().Get("listenKey") == "fixture-key" {
						deleted.Store(true)
					} else {
						t.Errorf("unexpected listen-key method %s", r.Method)
					}
				case "/swap-market":
					if r.URL.Query().Get("listenKey") != "fixture-key" {
						t.Error("WebSocket did not get the listen key")
					}
					ws, err := websocket.Accept(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = ws.CloseNow() }()
					send := func(value string) bool {
						var frame bytes.Buffer
						writer := gzip.NewWriter(&frame)
						if _, err := writer.Write([]byte(value)); err != nil {
							t.Error(err)
							return false
						}
						if err := writer.Close(); err != nil {
							t.Error(err)
							return false
						}
						return ws.Write(ctx, websocket.MessageBinary, frame.Bytes()) == nil
					}
					if !send(`{"e":"SNAPSHOT","E":1700000000000,"ac":{}}`) || !send("Ping") {
						return
					}
					if _, pong, err := ws.Read(ctx); err != nil || string(pong) != "Pong" {
						t.Errorf("heartbeat: got %q, %v", pong, err)
						return
					}
					for i, name := range []string{"TRADE_UPDATE", "ORDER_TRADE_UPDATE"} {
						if !send(fmt.Sprintf(`{"e":%q,"E":%d,"o":{"s":"DOGE-USDT","c":%q,"i":%s,"S":"BUY","o":"LIMIT","q":"35","z":"35.0000","X":"FILLED","x":"TRADE","T":%d,"td":%d}}`,
							name, now.UnixMilli(), strings.ToLower(clientID), brokerID, now.UnixMilli(), i+1)) {
							return
						}
					}
					_, _, _ = ws.Read(ctx)
				case "/openApi/swap/v2/trade/order":
					orderReads.Add(1)
					fmt.Fprintf(w, `{"code":0,"data":{"orderId":%s,"positionID":"54321","clientOrderId":%q,"symbol":"DOGE-USDT","side":"BUY","type":"LIMIT","status":"FILLED","origQty":"35","executedQty":"35","time":%d}}`,
						brokerID, strings.ToLower(clientID), now.UnixMilli())
				case "/openApi/swap/v2/trade/allFillOrders":
					fillReads.Add(1)
					q := r.URL.Query()
					if q.Get("orderId") != brokerID || q.Get("tradingUnit") != "COIN" ||
						q.Get("startTs") == "" || q.Get("endTs") == "" {
						t.Error("fill lookup did not use the stream's exact order ID and base units")
					}
					fmt.Fprintf(w, `{"code":0,"data":{"fill_orders":[{"orderId":%s,"symbol":"DOGE-USDT","side":"BUY","volume":"35","price":"0.05753","filledTime":%q}]}}`,
						brokerID, now.UTC().Format(time.RFC3339Nano))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			env.base = server.URL
			env.stream = "ws" + strings.TrimPrefix(server.URL, "http") + "/swap-market"
			a := newAPI(env, creds.Pair{KeyID: "key", SecretKey: "secret"}, clock.System{})
			hints := make(chan perps.FillHint, 2)
			done := make(chan struct{})
			go func() {
				defer close(done)
				a.StreamOrders(ctx, func(bool) {}, func(hint perps.FillHint) { hints <- hint })
			}()
			defer func() {
				cancel()
				<-done
			}()
			var hint perps.FillHint
			select {
			case hint = <-hints:
			case <-ctx.Done():
				t.Fatal("no fill event received")
			}
			select {
			case alias := <-hints:
				if hint != alias {
					t.Errorf("two notifications for the same fill gave different order evidence: %+v, %+v", hint, alias)
				}
			case <-ctx.Done():
				t.Fatal("no alternate fill event received")
			}
			req := exec.OrderRequest{ClientOrderID: clientID, Symbol: "PERP.BINGX.DOGE-USDT", Side: exec.SideBuy,
				Type: exec.TypeLimit, Qty: 35, Perpetual: &exec.PerpetualOrder{Multiplier: 1}}
			report, err := a.ObserveFill(ctx, req, now.UnixMilli(), hint)
			if err != nil || report.Status != exec.StatusFilled || len(report.Fills) != 1 ||
				report.Fills[0].Qty != 35 || report.Fills[0].Price != 0.05753 {
				t.Fatalf("unexpected stream-triggered observation: %+v, %v", report, err)
			}
			if orderReads.Load() != 0 || fillReads.Load() != 1 {
				t.Fatal("normal fill required more than one fill-history request")
			}
			req.Perpetual.LocalProtection = true
			report, err = a.ObserveFill(ctx, req, now.UnixMilli(), hint)
			if err != nil || report.PositionID != "54321" || orderReads.Load() != 1 {
				t.Fatalf("protected entry did not preserve signed position evidence: %+v, %v", report, err)
			}
			cancel()
			<-done
			if !deleted.Load() {
				t.Error("listen key was not released")
			}
		})
	}
}
