package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"golang.org/x/sync/errgroup"
)

// connectTimeout bounds the dial -> auth -> listen handshake as a single
// unit. Alpaca's WS protocol gives no auth-response deadline of its own, so
// without this a server that accepts the dial but never answers auth would
// hang the session goroutine forever; connectTimeout turns that into a
// bounded failure run's reconnect/backoff loop can retry. Deliberately does
// not cover the steady-state read loop that follows a successful
// handshake — see defaultPingInterval/defaultPongTimeout for that.
const connectTimeout = 15 * time.Second

// defaultPingInterval is how often the pinger goroutine sends a WS ping
// once a session reaches steady state. Alpaca's trade_updates stream has no
// server-side ping/pong of its own, and an idle gap (no order activity) is
// the normal case, not a failure — so liveness can't be judged by "did Read
// get a data frame within N seconds" (that made every idle session
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

// wsClient is Alpaca's `trade_updates` WebSocket client: it holds the
// connection open, runs the auth->listen handshake, and dispatches decoded
// trade_updates events via onUpdate. It never normalizes — normalizeUpdate
// (Task 12) and the Adapter (Task 15) own that.
//
// The one protocol quirk this client is built around: Alpaca's paper
// endpoint sends trade_updates frames as WebSocket BINARY frames, while live
// sends TEXT frames — the JSON payload is identical either way. readFrame
// deliberately discards the frame's message type and only looks at the
// bytes, so decoding is opcode-agnostic and correct on both environments.
type wsClient struct {
	url, keyID, secret string
	clk                clock.Clock
	onUpdate           func(tradeUpdate)
	onConn             func(up bool)

	// pingInterval overrides defaultPingInterval; tests set this short
	// (e.g. 50ms) so the pinger cycles fast instead of waiting a real 15s.
	pingInterval time.Duration
	// pongTimeout overrides defaultPongTimeout; tests set this short so a
	// simulated dead peer is detected without a real 10s wait.
	pongTimeout time.Duration
	// bo is the reconnect backoff policy (netx.Backoff, Task 5). It lives on
	// the struct rather than as a run()-local var so its state persists
	// across sessions for the lifetime of one run(ctx) call, and so tests
	// can shrink Min/Max directly — same idiom as pingInterval/pongTimeout.
	bo netx.Backoff
}

// newWSClient builds a client for the given trade_updates WS URL. The
// callbacks are invoked from the run(ctx) goroutine, never concurrently with
// each other.
func newWSClient(wsURL, keyID, secret string, clk clock.Clock, onUpdate func(tradeUpdate), onConn func(bool)) *wsClient {
	return &wsClient{
		url:          wsURL,
		keyID:        keyID,
		secret:       secret,
		clk:          clk,
		onUpdate:     onUpdate,
		onConn:       onConn,
		pingInterval: defaultPingInterval,
		pongTimeout:  defaultPongTimeout,
		bo:           netx.Backoff{Min: time.Second, Max: 30 * time.Second},
	}
}

// run drives connect -> auth -> listen -> read, reconnecting with jittered
// backoff (Task 5's netx.Backoff) on any session error, until ctx is done.
func (w *wsClient) run(ctx context.Context) {
	for ctx.Err() == nil {
		start := w.clk.Now()
		err := w.session(ctx)
		w.onConn(false)
		if err != nil && ctx.Err() == nil {
			slog.Warn("alpaca: trade_updates WS session ended; reconnecting", "err", err)
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
// period) must keep climbing the backoff, and only a demonstrably long-lived
// session earns the reset back to Min.
func resetBackoffIfHealthy(bo *netx.Backoff, sessionDuration time.Duration) {
	if sessionDuration >= bo.Max {
		bo.Reset()
	}
}

// session runs one connection end to end: dial, auth, listen, then steady
// state (concurrent read + ping) until the socket errors, an error frame
// arrives, a ping goes unanswered, or ctx is done.
func (w *wsClient) session(ctx context.Context) error {
	// Bounded connect phase: dial, auth, and listen all share one deadline,
	// so a peer that accepts the dial but never answers auth (or never acks
	// listen) fails this session instead of hanging it forever. cancel is
	// called explicitly once the handshake completes, below, rather than
	// left to the deferred call — the connect-phase deadline must not carry
	// over into the steady-state read/ping loop that follows.
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	c, _, err := websocket.Dial(cctx, w.url, nil)
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()

	authMsg, err := json.Marshal(map[string]string{
		"action": "auth",
		"key":    w.keyID,
		"secret": w.secret,
	})
	if err != nil {
		return err
	}
	if err := c.Write(cctx, websocket.MessageText, authMsg); err != nil {
		return err
	}
	if err := w.awaitAuthorized(cctx, c); err != nil {
		return err
	}

	listenMsg, err := json.Marshal(map[string]any{
		"action": "listen",
		"data": map[string]any{
			"streams": []string{"trade_updates"},
		},
	})
	if err != nil {
		return err
	}
	if err := c.Write(cctx, websocket.MessageText, listenMsg); err != nil {
		return err
	}
	cancel() // release the connect-phase deadline; steady state uses ctx directly

	w.onConn(true)

	// Steady state: readLoop and pinger run concurrently in the same
	// errgroup. Whichever errors first cancels gctx, which unblocks the
	// other's blocking call (Read or Ping) so the session tears down
	// cleanly instead of leaking a goroutine.
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
				return fmt.Errorf("alpaca: trade_updates WS ping timeout: %w", err)
			}
		}
	}
}

// wsEnvelope is the discriminant envelope for every frame on this stream:
// handshake responses ({"stream":"authorization",...}, {"stream":"listening",...}),
// data pushes ({"stream":"trade_updates",...}), and error frames (either
// {"stream":"error",...} or {"action":"error",...}, per the brief — Alpaca's
// docs and observed captures aren't fully consistent on which shape an error
// arrives in, so both are treated the same).
type wsEnvelope struct {
	Stream string          `json:"stream"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

func (e wsEnvelope) isError() bool {
	return e.Action == "error" || e.Stream == "error"
}

// authStatus is the {"data":{"status":...}} shape of an authorization
// response frame.
type authStatus struct {
	Status string `json:"status"`
}

// awaitAuthorized reads frames until it sees the authorization response.
// Any error frame, or an authorization response whose status isn't
// "authorized", ends the session (run reconnects with backoff — retrying a
// bad key/secret indefinitely is a real operational risk, but Alpaca's WS
// protocol gives no distinct terminal-vs-transient signal here the way
// TradeZero's FAILED_AUTH status does, so this client treats it like any
// other session error rather than guessing). ctx here is session's bounded
// connect-phase context, so a peer that never answers auth fails this
// session rather than hanging it.
func (w *wsClient) awaitAuthorized(ctx context.Context, c *websocket.Conn) error {
	for {
		data, err := w.readFrame(ctx, c)
		if err != nil {
			return err
		}
		var env wsEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("alpaca: trade_updates WS undecodable frame during auth", "err", err)
			continue
		}
		if env.isError() {
			return fmt.Errorf("alpaca: trade_updates WS error frame during auth: %s", data)
		}
		if env.Stream != "authorization" {
			continue // ignore any stray frame before the authorization response
		}
		var st authStatus
		if err := json.Unmarshal(env.Data, &st); err != nil {
			return fmt.Errorf("alpaca: decode authorization frame: %w", err)
		}
		if st.Status != "authorized" {
			return fmt.Errorf("alpaca: trade_updates WS authorization failed: status=%s", st.Status)
		}
		return nil
	}
}

// readFrame reads one frame, discarding the WebSocket message type
// entirely: Alpaca's paper endpoint sends trade_updates as BINARY frames,
// live sends TEXT, and the JSON payload is identical either way, so the
// opcode carries no information this client needs. It blocks on ctx alone —
// no per-read staleness deadline — since steady-state idle gaps are the
// normal case now that liveness is judged by pinger's ping/pong, not by how
// recently a data frame arrived.
func (w *wsClient) readFrame(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readLoop reads frames until the connection errors or an error frame
// arrives, dispatching every trade_updates push to onUpdate. Any other
// stream ("listening", or an unrecognized future stream) is ignored rather
// than treated as an error, so a harmless post-listen confirmation frame
// never tears down the session. ctx is the errgroup's gctx: it's canceled
// as soon as pinger (running concurrently) reports a ping timeout, which
// unblocks the Read below.
func (w *wsClient) readLoop(ctx context.Context, c *websocket.Conn) error {
	for {
		data, err := w.readFrame(ctx, c)
		if err != nil {
			return err
		}

		var env wsEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("alpaca: trade_updates WS undecodable frame", "err", err)
			continue
		}
		if env.isError() {
			return fmt.Errorf("alpaca: trade_updates WS error frame: %s", data)
		}
		if env.Stream != "trade_updates" {
			continue
		}

		var tu tradeUpdate
		if err := json.Unmarshal(env.Data, &tu); err != nil {
			slog.Warn("alpaca: trade_updates WS undecodable trade_updates payload", "err", err)
			continue
		}
		w.onUpdate(tu)
	}
}
