package netx

import (
	"net"
	"net/http"
	"time"
)

// NewHTTPClient returns an *http.Client with a warm keep-alive connection pool.
// Alpaca's cold TLS is ~430ms vs ~210ms warm, so reuse is mandatory; TZ benefits
// equally.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}
