package tradezero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"golang.org/x/sync/errgroup"
)

// connectTimeout bounds the dial -> handshake -> subscribe sequence as a
// single unit. TZ's Portfolio WS gives no distinct deadline of its own for
// any handshake step, so without this a peer that accepts the dial but never
// advances past PENDING_AUTH (or never acks subscribe) would hang the
// session goroutine forever; connectTimeout turns that into a bounded
// failure run's reconnect/backoff loop can retry. Deliberately does not
// cover the steady-state read loop that follows a successful handshake — see
// defaultPingInterval/defaultPongTimeout for that.
const connectTimeout = 15 * time.Second

// defaultPingInterval is how often the pinger goroutine sends a WS ping once
// a session reaches steady state. TZ's Portfolio WS has no server-side
// ping/pong of its own, and an idle gap (no order/position activity) is the
// normal case, not a failure — so liveness can't be judged by "did Read get
// a data frame within N seconds" (that made every idle session
// indistinguishable from a dead one). It must be judged by whether a ping we
// send gets answered instead.
const defaultPingInterval = 15 * time.Second

// defaultPongTimeout bounds how long the pinger waits for a pong to a ping
// it sent before deciding the connection is dead. coder/websocket's
// (*Conn).Ping blocks until the pong control frame is consumed by a
// concurrent Read call — the pong itself never unblocks Read, it's handled
// internally by the read machinery — so a pong can only ever arrive while
// readLoop's Read is in flight. This is why pinger and readLoop must run
// concurrently (see session's errgroup) rather than one after the other.
const defaultPongTimeout = 10 * time.Second

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

	// pingInterval overrides defaultPingInterval; tests set this short
	// (e.g. 50ms) so the pinger cycles fast instead of waiting a real 15s.
	pingInterval time.Duration
	// pongTimeout overrides defaultPongTimeout; tests set this short so a
	// simulated dead peer is detected without a real 10s wait.
	pongTimeout time.Duration
	// bo is the reconnect backoff policy. It lives on the struct rather than
	// as a run()-local var so its state persists across sessions for the
	// lifetime of one run(ctx) call, and so tests can shrink Min/Max
	// directly — same idiom as pingInterval/pongTimeout.
	bo netx.Backoff
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
		pingInterval: defaultPingInterval,
		pongTimeout:  defaultPongTimeout,
		bo:           netx.Backoff{Min: time.Second, Max: 30 * time.Second},
	}
}

// run drives connect -> handshake -> subscribe -> read, reconnecting with
// jittered backoff on any transport error, until ctx is done or the server
// reports FAILED_AUTH (fatal — see errFailedAuth).
func (w *wsClient) run(ctx context.Context) {
	for ctx.Err() == nil {
		start := w.clk.Now()
		err := w.session(ctx)
		w.onConn(false)
		if errors.Is(err, errFailedAuth) {
			slog.Error("tradezero: portfolio WS auth rejected; not reconnecting", "err", err)
			return // never reconnect on bad keys
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("tradezero: portfolio WS session ended; reconnecting", "err", err)
		}
		resetBackoffIfHealthy(&w.bo, w.clk.Now().Sub(start))
		select {
		case <-ctx.Done():
			return
		case <-w.clk.After(w.bo.Next()):
		}
	}
}

// resetBackoffIfHealthy resets bo when the session that just ended lasted at
// least bo.Max: long enough to have been a genuinely healthy, actively-pinged
// connection rather than a connect-then-drop loop. Duration-based
// deliberately: a rapid sequence of short-lived sessions (a real flapping
// period, or repeated FAILED_AUTH — though that path returns before ever
// reaching this call) must keep climbing the backoff, and only a
// demonstrably long-lived session earns the reset back to Min.
func resetBackoffIfHealthy(bo *netx.Backoff, sessionDuration time.Duration) {
	if sessionDuration >= bo.Max {
		bo.Reset()
	}
}

// session runs one connection end to end: dial, handshake, subscribe, then
// steady state (concurrent read + ping) until the socket errors, a ping goes
// unanswered, FAILED_AUTH arrives, or ctx is done.
func (w *wsClient) session(ctx context.Context) error {
	// Bounded connect phase: dial, handshake, and subscribe all share one
	// deadline, so a peer that accepts the dial but never advances the
	// handshake (or never acks subscribe) fails this session instead of
	// hanging it forever. cancel is called explicitly once the handshake
	// completes, below, rather than left to the deferred call — the
	// connect-phase deadline must not carry over into the steady-state
	// read/ping loop that follows.
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	c, _, err := websocket.Dial(cctx, w.url, nil)
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()

	// 1) PENDING_AUTH
	if err := w.awaitStatus(cctx, c, "PENDING_AUTH"); err != nil {
		return err
	}
	// 2) send key/secret
	auth, err := json.Marshal(map[string]string{"key": w.keyID, "secret": w.secret})
	if err != nil {
		return err
	}
	if err := c.Write(cctx, websocket.MessageText, auth); err != nil {
		return err
	}
	// 3) CONNECTED (or FAILED_AUTH)
	if err := w.awaitStatus(cctx, c, "CONNECTED"); err != nil {
		return err
	}
	if err := w.subscribe(cctx, c); err != nil {
		return err
	}
	cancel() // release the connect-phase deadline; steady state uses ctx directly

	w.onConn(true)

	// Steady state: readLoop and pinger run concurrently in the same
	// errgroup. Whichever errors first cancels gctx, which unblocks the
	// other's blocking call (Read or Ping) so the session tears down
	// cleanly instead of leaking a goroutine. A late FAILED_AUTH surfacing
	// from readLoop (see readLoop's comment) propagates out of g.Wait()
	// unchanged, so run()'s errors.Is(err, errFailedAuth) check still sees
	// it and ends the client for good.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return w.readLoop(gctx, c) })
	g.Go(func() error { return w.pinger(gctx, c) })
	return g.Wait()
}

// pinger sends a WS ping every pingInterval and reports an error if a pong
// doesn't arrive within pongTimeout — the only liveness signal available
// once idle-but-alive periods can no longer be distinguished from a dead
// connection by Read timing alone (see defaultPongTimeout). Runs until ctx
// is done or a ping times out; must run concurrently with readLoop (same
// gctx, same errgroup) or its pong can never be consumed.
func (w *wsClient) pinger(ctx context.Context, c *websocket.Conn) error {
	t := w.clk.NewTicker(w.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C():
			pctx, cancel := context.WithTimeout(ctx, w.pongTimeout)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return fmt.Errorf("tradezero: portfolio WS ping timeout: %w", err)
			}
		}
	}
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

// readFrame reads one frame. It blocks on ctx alone — no per-read staleness
// deadline — since steady-state idle gaps are the normal case now that
// liveness is judged by pinger's ping/pong, not by how recently a data frame
// arrived.
func (w *wsClient) readFrame(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	_, data, err := c.Read(ctx)
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

// readLoop reads frames until the connection errors, and dispatches
// action:"update" frames to onOrder/onPosition. It also keeps handling the
// system envelope post-handshake: TERMINATED/INVALID_DATA keep the socket
// open (log + resend subscribe) rather than tearing it down, and a late
// FAILED_AUTH is still fatal — it returns errFailedAuth same as the
// handshake path, which propagates out of session's errgroup unchanged (see
// session's comment) so run() still ends the client for good. ctx is the
// errgroup's gctx: it's canceled as soon as pinger (running concurrently)
// reports a ping timeout, which unblocks the Read below.
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
