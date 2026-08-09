// server.go wires the real github.com/coder/websocket library to the conn
// (Task 7) and hub (Task 6) built and tested against fakes, and serves the
// built ui/dist SPA for the same origin.
package uihub

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/webui"
)

// defaultWriteTimeout bounds a single ws.Write call in production (see
// conn.go's writeLoop): a peer that can't accept even one already-queued
// frame within this window is treated as wedged, not merely slow, and the
// connection is dropped instead of blocking writeLoop indefinitely. Generous
// on purpose -- this is a last-resort backstop for a genuinely stuck socket,
// not a latency budget; tests construct conns with a much shorter timeout
// directly (see conn_test.go).
const defaultWriteTimeout = 5 * time.Second

type ServerConfig struct {
	DistDir string // built ui/dist; empty => no static serving (dev proxies /ws)
	OutBuf  int    // per-connection outbound queue depth
}

type Server struct {
	hub    *Hub
	cmd    commandHandler
	qry    queryHandler
	cfg    ServerConfig
	nextID atomic.Uint64
	connWG sync.WaitGroup // tracks every accepted conn.run() from accept to teardown
}

func NewServer(h *Hub, cmd commandHandler, qry queryHandler, cfg ServerConfig) *Server {
	if cfg.OutBuf <= 0 {
		cfg.OutBuf = 1024
	}
	return &Server{hub: h, cmd: cmd, qry: qry, cfg: cfg}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	if s.cfg.DistDir != "" {
		// dev/-dist override: serve the on-disk directory exactly as before.
		mux.Handle("/", s.spaHandler(os.DirFS(s.cfg.DistDir)))
	} else if fsys, ok := webui.Dist(); ok {
		// Release (embed_ui) build with no override: serve the UI baked into
		// the binary. The default build's webui.Dist() always returns
		// (nil, false), so "/" stays unrouted exactly as today.
		mux.Handle("/", s.spaHandler(fsys))
	}
	return mux
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	// Localhost app: accept same-origin plus the Vite dev origin. InsecureSkipVerify
	// is acceptable because the server binds 127.0.0.1 only (see main).
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20) // 1 MiB frame cap
	id := s.nextID.Add(1)
	conn := newConn(id, coderSocket{c: c}, s.hub, s.cmd, s.qry, s.cfg.OutBuf, defaultWriteTimeout)
	// Add(1) before Register: Wait() must count this connection from the
	// instant it exists, not after it's (possibly unsuccessfully) handed to the
	// hub -- otherwise a Wait() call landing in the gap between accept and
	// Register could observe a zero counter and return before this goroutine
	// has even started running.
	s.connWG.Add(1)
	defer s.connWG.Done()
	s.hub.Register(conn)
	conn.run(r.Context()) // blocks until the socket closes; run() calls hub.Unregister
}

// Wait blocks until every conn.run() this server has started has returned --
// i.e. no client's dispatch() (and therefore no SetConfig call reaching the
// store) can still be in flight. main.go must call this after httpSrv.Shutdown
// and before st.Close(), since Hub.Run's <-ctx.Done() branch is what actually
// tells live connections to close (r.Context() is tied to the individual HTTP
// request, not to the top-level ctx, and http.Server.Shutdown does not wait on
// hijacked WebSocket connections).
func (s *Server) Wait() {
	s.connWG.Wait()
}

// spaHandler serves files from fsys, falling back to index.html for unknown
// paths. fsys is either os.DirFS(DistDir) (dev/-dist override) or the
// embedded webui.Dist() filesystem (release build) -- both are plain
// fs.FS values, so the fallback logic is identical either way.
func (s *Server) spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "."
		}
		if info, err := fs.Stat(fsys, p); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}

// coderSocket adapts *websocket.Conn to the wsSocket interface conn expects.
type coderSocket struct {
	c *websocket.Conn
}

func (s coderSocket) Read(ctx context.Context) ([]byte, error) {
	_, b, err := s.c.Read(ctx)
	return b, err
}

func (s coderSocket) Write(ctx context.Context, b []byte) error {
	return s.c.Write(ctx, websocket.MessageText, b)
}

func (s coderSocket) Close(code int, reason string) error {
	return s.c.Close(websocket.StatusCode(code), reason)
}
