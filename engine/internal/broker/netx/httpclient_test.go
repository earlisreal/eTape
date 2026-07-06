package netx

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClient_KeepAliveTuned(t *testing.T) {
	c := NewHTTPClient(5 * time.Second)
	if c.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.MaxIdleConnsPerHost < 4 || tr.IdleConnTimeout == 0 {
		t.Fatalf("keep-alive not tuned: idlePerHost=%d idleTimeout=%v", tr.MaxIdleConnsPerHost, tr.IdleConnTimeout)
	}
}
