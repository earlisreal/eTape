package tradezero

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

// mockTZ serves the 3-step handshake then pushes one order update.
func mockTZ(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx) // {"key":..,"secret":..}
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe payload
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","userOrderId":"2TZ00001:ETx","symbol":"AAPL","orderStatus":"New","orderQuantity":10,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`))
		<-ctx.Done()
	}))
}

func TestWS_HandshakeAndOrderDispatch(t *testing.T) {
	srv := mockTZ(t)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):] // http->ws

	var mu sync.Mutex
	var got []tzOrder
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(o tzOrder) { mu.Lock(); got = append(got, o); mu.Unlock() },
		func(tzPosition) {}, func(bool) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Symbol != "AAPL" {
				t.Fatalf("order symbol = %q", got[0].Symbol)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("did not receive the order update within timeout")
}
