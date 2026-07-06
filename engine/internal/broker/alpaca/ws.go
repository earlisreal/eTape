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
)

// defaultStaleTimeout bounds how long the client waits for any frame
// (handshake response or data push) before deciding the connection is dead.
// A socket that silently stopped delivering data (e.g. the server hangs
// without ever closing) would otherwise hang the read loop forever.
const defaultStaleTimeout = 30 * time.Second

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
	// staleTimeout overrides defaultStaleTimeout; TestWS_StaleReadTriggersReconnect
	// sets this to a short duration directly on the returned struct to
	// exercise the stale-read path without a real 30s wait.
	staleTimeout time.Duration
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
		staleTimeout: defaultStaleTimeout,
	}
}

// run drives connect -> auth -> listen -> read, reconnecting with jittered
// backoff (Task 5's netx.Backoff) on any session error, until ctx is done.
func (w *wsClient) run(ctx context.Context) {
	bo := netx.Backoff{Min: time.Second, Max: 30 * time.Second}
	for ctx.Err() == nil {
		err := w.session(ctx)
		w.onConn(false)
		if err != nil && ctx.Err() == nil {
			slog.Warn("alpaca: trade_updates WS session ended; reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-w.clk.After(bo.Next()):
		}
	}
}

// session runs one connection end to end: dial, auth, listen, then read
// until the socket errors, goes stale, reports an error frame, or ctx is
// done.
func (w *wsClient) session(ctx context.Context) error {
	c, _, err := websocket.Dial(ctx, w.url, nil)
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
	if err := c.Write(ctx, websocket.MessageText, authMsg); err != nil {
		return err
	}
	if err := w.awaitAuthorized(ctx, c); err != nil {
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
	if err := c.Write(ctx, websocket.MessageText, listenMsg); err != nil {
		return err
	}

	w.onConn(true)
	return w.readLoop(ctx, c)
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
// other session error rather than guessing).
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

// readFrame reads one frame with a staleness deadline, discarding the
// WebSocket message type entirely: Alpaca's paper endpoint sends
// trade_updates as BINARY frames, live sends TEXT, and the JSON payload is
// identical either way, so the opcode carries no information this client
// needs.
func (w *wsClient) readFrame(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, w.staleTimeout)
	defer cancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readLoop reads frames until the connection errors, goes stale, or an error
// frame arrives, dispatching every trade_updates push to onUpdate. Any other
// stream ("listening", or an unrecognized future stream) is ignored rather
// than treated as an error, so a harmless post-listen confirmation frame
// never tears down the session.
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
