package venueprobe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestPerpetualProbesSignReadOnlyAccountRequests(t *testing.T) {
	for _, tc := range []struct {
		config              perpetualProbe
		name, balance, mode string
		oneWay              bool
	}{
		{binanceProbe, "Binance", `[{"asset":"USDT","availableBalance":"100"}]`, `{"dualSidePosition":false}`, true},
		{bingXProbe, "BingX", `{"code":0,"data":[{"asset":"USDT","availableMargin":"100"}]}`,
			`{"code":0,"data":{"dualSidePosition":true}}`, false},
		{bingXProbe, "BingX quoted one-way", `{"code":0,"data":[{"asset":"USDT","availableMargin":"100"}]}`,
			`{"code":0,"data":{"dualSidePosition":"false"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload, signature, found := strings.Cut(r.URL.RawQuery, "&signature=")
				mac := hmac.New(sha256.New, []byte("fake-secret"))
				_, _ = mac.Write([]byte(payload))
				if r.Method != "GET" || r.Header.Get(config.keyHeader) != "fake-key" || !found ||
					payload != "recvWindow=5000&timestamp=1700000000000" || signature != hex.EncodeToString(mac.Sum(nil)) {
					t.Errorf("invalid request to %s", r.URL.Path)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case config.balancePath:
					_, _ = fmt.Fprint(w, tc.balance)
				case config.modePath:
					_, _ = fmt.Fprint(w, tc.mode)
				default:
					t.Errorf("unexpected signed request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			config.base = server.URL
			got, err := config.inspect(context.Background(), clock.NewFake(time.UnixMilli(1700000000000)),
				creds.Pair{KeyID: "fake-key", SecretKey: "fake-secret"})
			if err != nil || got != tc.oneWay {
				t.Fatalf("one-way mode = %v, %v", got, err)
			}
		})
	}
}
