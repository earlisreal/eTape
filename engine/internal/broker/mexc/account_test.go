package mexc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestVerifyAccountUsesSignedReads(t *testing.T) {
	const key, secret, timestamp = "test-key", "test-secret", "1700000000000"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(key + timestamp + r.URL.RawQuery))
		signature := hex.EncodeToString(mac.Sum(nil))
		if r.Method != http.MethodGet || r.Header.Get("ApiKey") != key || r.Header.Get("Request-Time") != timestamp ||
			r.Header.Get("Signature") != signature {
			t.Errorf("unexpected authentication for %s", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/private/account/assets":
			_, _ = fmt.Fprint(w, `{"success":true,"code":0,"data":[{"currency":"USDT","availableBalance":100}]}`)
		case "/api/v1/private/position/position_mode":
			_, _ = fmt.Fprint(w, `{"success":true,"code":0,"data":2}`)
		case "/api/v1/private/position/open_positions":
			_, _ = fmt.Fprint(w, `{"success":true,"code":0,"data":[{"holdVol":"2"},{"holdVol":0}]}`)
		case "/api/v1/private/stoporder/open_orders":
			_, _ = fmt.Fprint(w, `{"success":true,"code":0,"data":[{"state":1,"isFinished":0},{"state":3,"isFinished":1}]}`)
		case "/api/v1/private/order/list/open_orders":
			if r.URL.Query().Get("page_num") != "1" || r.URL.Query().Get("page_size") != "100" {
				t.Errorf("unexpected current orders query %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `{"success":true,"code":0,"data":[{"orderId":"750330744886358528","state":2}]}`)
		default:
			t.Errorf("unexpected account endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := client{base: server.URL, http: server.Client(), clock: clock.NewFake(time.UnixMilli(1700000000000)),
		creds: creds.Pair{KeyID: key, SecretKey: secret}}
	result, err := c.verifyAccount(context.Background())
	if err != nil || result != (AccountAccess{OneWay: true, OpenPositions: 1, WorkingOrders: 1, ActiveTPSL: 1}) {
		t.Fatalf("verify account: %+v %v", result, err)
	}
}
