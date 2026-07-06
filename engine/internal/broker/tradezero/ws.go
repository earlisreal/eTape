package tradezero

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

// defaultStaleTimeout bounds how long the client waits for any frame
// (handshake status or data push) before deciding the connection is dead.
// TZ's Portfolio WS has no server-side ping/pong, so this timer is the only
// liveness signal — a socket that silently stopped delivering data would
// otherwise hang the read loop forever.
const defaultStaleTimeout = 30 * time.Second

// errFailedAuth signals bad credentials. It is the one session error that
// must never trigger a reconnect: retrying with the same key/secret would
// just fail again, forever, burning the backoff loop for nothing.
var errFailedAuth = errors.New("tradezero: FAILED_AUTH")

// wsClient is the TradeZero Portfolio WebSocket client: it holds the
// connection open, runs the 3-step handshake, subscribes to Order+Position
// pushes, and dispatches decoded frames via callbacks. It never normalizes —
// normalizeOrder (Task 7) and the Adapter (Task 10) own that.
type wsClient struct {
	url, accountID, keyID, secret string
	clk                           clock.Clock
	onOrder                       func(tzOrder)
	onPosition                    func(tzPosition)
	onConn                        func(up bool)
	// staleTimeout overrides defaultStaleTimeout; tests set this to a short
	// duration (e.g. 50ms) directly on the returned struct to exercise the
	// stale-read reconnect path without a real 30s wait.
	staleTimeout time.Duration
}

// newWSClient builds a client for the given Portfolio-WS URL and account. The
// callbacks are invoked from the run(ctx) goroutine, never concurrently with
// each other.
func newWSClient(url, accountID, keyID, secret string, clk clock.Clock, onOrder func(tzOrder), onPosition func(tzPosition), onConn func(up bool)) *wsClient {
	return &wsClient{
		url:          url,
		accountID:    accountID,
		keyID:        keyID,
		secret:       secret,
		clk:          clk,
		onOrder:      onOrder,
		onPosition:   onPosition,
		onConn:       onConn,
		staleTimeout: defaultStaleTimeout,
	}
}

// run drives connect -> handshake -> subscribe -> read, reconnecting with
// jittered backoff on any transport error, until ctx is done or the server
// reports FAILED_AUTH (fatal — see errFailedAuth).
func (w *wsClient) run(ctx context.Context) {
	bo := netx.Backoff{Min: time.Second, Max: 30 * time.Second}
	for ctx.Err() == nil {
		err := w.session(ctx)
		w.onConn(false)
		if errors.Is(err, errFailedAuth) {
			slog.Error("tradezero: portfolio WS auth rejected; not reconnecting", "err", err)
			return // never reconnect on bad keys
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("tradezero: portfolio WS session ended; reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-w.clk.After(bo.Next()):
		}
	}
}

// session runs one connection end to end: dial, handshake, subscribe, then
// read until the socket errors, goes stale, or ctx is done.
func (w *wsClient) session(ctx context.Context) error {
	c, _, err := websocket.Dial(ctx, w.url, nil)
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()

	// 1) PENDING_AUTH
	if err := w.awaitStatus(ctx, c, "PENDING_AUTH"); err != nil {
		return err
	}
	// 2) send key/secret
	auth, err := json.Marshal(map[string]string{"key": w.keyID, "secret": w.secret})
	if err != nil {
		return err
	}
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return err
	}
	// 3) CONNECTED (or FAILED_AUTH)
	if err := w.awaitStatus(ctx, c, "CONNECTED"); err != nil {
		return err
	}
	if err := w.subscribe(ctx, c); err != nil {
		return err
	}
	w.onConn(true)
	return w.readLoop(ctx, c)
}

func (w *wsClient) subscribe(ctx context.Context, c *websocket.Conn) error {
	sub, err := json.Marshal(map[string]any{
		"accountId":     w.accountID,
		"subscriptions": []string{"Order", "Position"},
	})
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, sub)
}

// tzSystemFrame is the envelope TZ uses for handshake/status frames.
type tzSystemFrame struct {
	System bool   `json:"@system"`
	Status string `json:"status"`
}

// awaitStatus reads frames until it sees the system envelope with the wanted
// status. FAILED_AUTH is always fatal (returns errFailedAuth) regardless of
// which status the caller is waiting for. TERMINATED and INVALID_DATA are
// non-fatal for the *connection* — the brief calls for resending the
// subscribe payload and continuing, which subscribe-bearing callers do by
// checking the returned sentinel; awaitStatus itself just keeps reading past
// them since neither is the status being awaited nor FAILED_AUTH. Any frame
// that isn't a system envelope is ignored (the server should not send data
// pushes before CONNECTED, but ignoring stray frames is cheap and robust).
func (w *wsClient) awaitStatus(ctx context.Context, c *websocket.Conn, want string) error {
	for {
		data, err := w.readFrame(ctx, c)
		if err != nil {
			return err
		}
		var f tzSystemFrame
		if err := json.Unmarshal(data, &f); err != nil || !f.System {
			continue
		}
		switch f.Status {
		case want:
			return nil
		case "FAILED_AUTH":
			return errFailedAuth
		case "TERMINATED", "INVALID_DATA":
			slog.Warn("tradezero: portfolio WS non-fatal status during handshake", "status", f.Status)
			continue
		}
	}
}

// readFrame reads one frame with a staleness deadline, so a socket that
// stops delivering anything (no server ping exists on this protocol) is
// detected and torn down rather than hung on forever.
func (w *wsClient) readFrame(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, w.staleTimeout)
	defer cancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// tzUpdateFrame is the discriminant envelope for post-subscribe pushes: the
// server tags data frames with action:"update" and either order or position
// fields flattened into the same object. tzOrder/tzPosition are decode-
// tolerant (unknown fields ignored), so it is enough to try decoding as
// each and check for a field that only appears on that shape.
type tzUpdateFrame struct {
	System bool   `json:"@system"`
	Status string `json:"status"`
	Action string `json:"action"`
}

// readLoop reads frames until the connection errors or goes stale, and
// dispatches action:"update" frames to onOrder/onPosition. It also keeps
// handling the system envelope post-handshake: TERMINATED/INVALID_DATA keep
// the socket open (log + resend subscribe) rather than tearing it down, and
// a late FAILED_AUTH is still fatal.
func (w *wsClient) readLoop(ctx context.Context, c *websocket.Conn) error {
	for {
		data, err := w.readFrame(ctx, c)
		if err != nil {
			return err
		}

		var env tzUpdateFrame
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("tradezero: portfolio WS undecodable frame", "err", err)
			continue
		}

		if env.System {
			switch env.Status {
			case "FAILED_AUTH":
				return errFailedAuth
			case "TERMINATED", "INVALID_DATA":
				slog.Warn("tradezero: portfolio WS non-fatal status; resubscribing", "status", env.Status)
				if err := w.subscribe(ctx, c); err != nil {
					return err
				}
			}
			continue
		}

		if env.Action != "update" {
			continue
		}
		w.dispatchUpdate(data)
	}
}

// dispatchUpdate decides whether an update frame is an order or a position
// push and calls the matching callback. An order push always carries
// userOrderId; a position push never does (it identifies by symbol+side
// only), so that field is the discriminant.
func (w *wsClient) dispatchUpdate(data []byte) {
	var probe struct {
		UserOrderID string `json:"userOrderId"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.UserOrderID != "" {
		var o tzOrder
		if err := json.Unmarshal(data, &o); err != nil {
			slog.Warn("tradezero: portfolio WS undecodable order update", "err", err)
			return
		}
		w.onOrder(o)
		return
	}
	var p tzPosition
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Warn("tradezero: portfolio WS undecodable position update", "err", err)
		return
	}
	w.onPosition(p)
}
