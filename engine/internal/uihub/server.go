// server.go wires the real github.com/coder/websocket library to the conn
// (Task 7) and hub (Task 6) built and tested against fakes, and serves the
// built ui/dist SPA for the same origin.
package uihub

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

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
		mux.Handle("/", s.spaHandler(s.cfg.DistDir))
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
	conn := newConn(id, coderSocket{c: c}, s.hub, s.cmd, s.qry, s.cfg.OutBuf)
	s.hub.Register(conn)
	s.connWG.Add(1)
	defer s.connWG.Done()
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

// spaHandler serves files from dir, falling back to index.html for unknown paths.
func (s *Server) spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
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
