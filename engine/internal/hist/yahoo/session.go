package yahoo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Session supplies the anonymous cookie and crumb required by Yahoo's quote APIs.
type Session struct {
	base      string
	cookieURL string
	userAgent string
	hc        *http.Client
	mu        sync.Mutex
	cookie    string
	crumb     string
}

func NewSession(base string, hc *http.Client, userAgent string) *Session {
	cookieURL := "https://fc.yahoo.com"
	if base == "" {
		base = defaultBase
	} else {
		base = strings.TrimRight(base, "/")
		cookieURL = base + "/fc.yahoo.com"
	}
	return &Session{base: base, cookieURL: cookieURL, hc: hc, userAgent: userAgent}
}

func (s *Session) Credentials(ctx context.Context) (cookie, crumb string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookie != "" && s.crumb != "" {
		return s.cookie, s.crumb, nil
	}
	cookie, err = s.fetchCookie(ctx)
	if err != nil {
		return "", "", err
	}
	crumb, err = s.fetchCrumb(ctx, cookie)
	if err != nil {
		return "", "", err
	}
	s.cookie, s.crumb = cookie, crumb
	return cookie, crumb, nil
}

// Invalidate only clears the rejected session, in case another caller refreshed it.
func (s *Session) Invalidate(cookie, crumb string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookie == cookie && s.crumb == crumb {
		s.cookie, s.crumb = "", ""
	}
}

func (s *Session) fetchCookie(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cookieURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", s.userAgent)
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	cookies := resp.Cookies()
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	if len(values) == 0 {
		return "", fmt.Errorf("yahoo session: no cookie from %s", s.cookieURL)
	}
	return strings.Join(values, "; "), nil
}

func (s *Session) fetchCrumb(ctx context.Context, cookie string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/test/getcrumb", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Cookie", cookie)
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	crumb := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || crumb == "" {
		return "", fmt.Errorf("yahoo session: crumb unavailable (HTTP %d)", resp.StatusCode)
	}
	return crumb, nil
}
