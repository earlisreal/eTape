package opend

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// ConnState is the connection lifecycle signal on State().
type ConnState int

const (
	ConnDown ConnState = iota
	ConnUp
)

func (s ConnState) String() string {
	if s == ConnUp {
		return "up"
	}
	return "down"
}

var (
	ErrRequestTimeout = errors.New("opend: request timed out")
	ErrNotConnected   = errors.New("opend: not connected")
)

// Options configures a Client. Zero values are filled with defaults in New.
type Options struct {
	Addr           string
	ClientID       string
	ClientVer      int32
	RequestTimeout time.Duration
	DialTimeout    time.Duration
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	Clock          clock.Clock
}

// Client is the OpenD connection: a supervised TCP session with request/response
// correlation and push dispatch. It holds no market-data domain state.
type Client struct {
	opt Options
	clk clock.Clock

	mu     sync.Mutex
	conn   net.Conn // current live conn; nil when down
	connID uint64
	kaInt  time.Duration
	sendMu sync.Mutex

	serial  serialGen
	pending *pending

	pushes chan Frame
	state  chan ConnState
}

// New builds a Client, filling defaults.
func New(opt Options) *Client {
	if opt.RequestTimeout == 0 {
		opt.RequestTimeout = 5 * time.Second
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}
	if opt.ReconnectMin == 0 {
		opt.ReconnectMin = time.Second
	}
	if opt.ReconnectMax == 0 {
		opt.ReconnectMax = 30 * time.Second
	}
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	if opt.ClientVer == 0 {
		opt.ClientVer = 100
	}
	if opt.ClientID == "" {
		opt.ClientID = "etape-engine"
	}
	return &Client{
		opt:     opt,
		clk:     opt.Clock,
		pending: newPending(),
		pushes:  make(chan Frame, 1024),
		state:   make(chan ConnState, 8),
	}
}

// Pushes yields frames with no matching in-flight request (dispatched by protoID
// by consumers). Plan 2 wraps this into typed FeedEvents.
func (c *Client) Pushes() <-chan Frame { return c.pushes }

// State yields connection up/down transitions.
func (c *Client) State() <-chan ConnState { return c.state }

// ConnID returns the OpenD-assigned connection ID (0 until InitConnect succeeds).
func (c *Client) ConnID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connID
}

// Request sends req as protoID and waits for the correlated response.
func (c *Client) Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error) {
	body, err := proto.Marshal(req)
	if err != nil {
		return Frame{}, err
	}
	serial := c.serial.next()
	ch := c.pending.register(serial)
	defer c.pending.cancel(serial) // no-op if already resolved

	if err := c.send(Encode(protoID, serial, body)); err != nil {
		return Frame{}, err
	}

	select {
	case f, ok := <-ch:
		if !ok {
			return Frame{}, ErrNotConnected // failAll closed it
		}
		return f, nil
	case <-c.clk.After(c.opt.RequestTimeout):
		return Frame{}, ErrRequestTimeout
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
}

func (c *Client) send(frame []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_, err := conn.Write(frame)
	return err
}

// serveConn runs one connection to completion: it spawns the reader, performs the
// InitConnect handshake, runs the keepalive loop, and returns the error that ended
// the session. Lifecycle helpers (initConnect, keepAliveLoop) are in lifecycle.go;
// the supervising Run loop is added below.
func (c *Client) serveConn(ctx context.Context, conn net.Conn) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.setConn(conn)
	// Close the socket on every exit path (ctx cancel, readErr, kaErr, or a
	// handshake/keepalive timeout where OpenD keeps TCP open but stops replying).
	// Without this the reader goroutine stays blocked in ReadFrame (io.ReadFull,
	// no deadline) and both it and the fd leak; closing unblocks ReadFrame, which
	// then returns an error and the reader exits. Single close on the way out —
	// no double-close.
	defer func() { _ = conn.Close() }()
	defer c.clearConn()

	readErr := make(chan error, 1)
	fr := NewFrameReader(conn)
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				readErr <- err
				return
			}
			if !c.pending.resolve(f.SerialNo, f) {
				select {
				case c.pushes <- f:
				case <-sctx.Done():
					return
				default:
					// push buffer full: drop. Plan 2's feed wrapper owns
					// coalescing/backpressure and forces a re-snapshot instead.
				}
			}
		}
	}()

	if err := c.initConnect(sctx); err != nil {
		return err
	}
	c.emit(ConnUp)
	defer c.emit(ConnDown)

	kaErr := make(chan error, 1)
	go c.keepAliveLoop(sctx, kaErr)

	select {
	case <-sctx.Done():
		return sctx.Err()
	case err := <-readErr:
		return err
	case err := <-kaErr:
		return err
	}
}

func (c *Client) setConn(conn net.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) clearConn() {
	c.mu.Lock()
	c.conn = nil
	c.connID = 0
	c.mu.Unlock()
	c.pending.failAll()
}

func (c *Client) setConnInfo(connID uint64, kaInterval time.Duration) {
	c.mu.Lock()
	c.connID = connID
	c.kaInt = kaInterval
	c.mu.Unlock()
}

// keepAliveInterval reads kaInt under lock. kaInt is written by setConnInfo on
// every fresh session (including reconnects on a reused *Client), so a prior
// session's keepAliveLoop goroutine can still be scheduled to run its startup
// read concurrently with a new session's initConnect — an unguarded read here
// raced with that write under -race.
func (c *Client) keepAliveInterval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kaInt
}

func (c *Client) emit(s ConnState) {
	select {
	case c.state <- s:
	default:
	}
}

// Run supervises the connection: dial → serve → (on any error) backoff → redial,
// until ctx is cancelled. It blocks; callers run it in a goroutine.
func (c *Client) Run(ctx context.Context) error {
	bo := newBackoff(c.opt.ReconnectMin, c.opt.ReconnectMax)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := c.dialOnce(ctx)
		if err == nil {
			bo.reset()
			// serveConn emits ConnUp on handshake and ConnDown on exit.
			_ = c.serveConn(ctx, conn)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.clk.After(bo.next()):
		}
	}
}

func (c *Client) dialOnce(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.opt.DialTimeout}
	return d.DialContext(ctx, "tcp", c.opt.Addr)
}
