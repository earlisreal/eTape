//go:build wails

package uihub

import (
	"context"
	"encoding/json"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type wailsStreamSocket struct {
	stream *application.StreamConn
}

func (s wailsStreamSocket) Read(context.Context) ([]byte, error) {
	return s.stream.Receive()
}

func (s wailsStreamSocket) Write(_ context.Context, b []byte) error {
	return s.stream.TrySend(b)
}

func (s wailsStreamSocket) Close(_ int, reason string) error {
	if reason == "engine stopped" || reason == "restarting" {
		kind := "stopping"
		if reason == "restarting" {
			kind = "restarting"
		}
		if frame, err := json.Marshal(struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}{Type: kind, Reason: reason}); err == nil {
			_ = s.stream.TrySend(frame)
		}
	}
	return s.stream.Close()
}

// HandleWailsStream adapts the already-admitted Wails stream to the same conn
// used by the legacy localhost WebSocket bridge. Hub owns registration,
// snapshots, ordering, coalescing, and disconnect cleanup for both transports.
func (s *Server) HandleWailsStream(stream *application.StreamConn) {
	id := s.nextID.Add(1)
	conn := newConn(id, wailsStreamSocket{stream: stream}, s.hub, s.cmd, s.qry, s.cfg.OutBuf, defaultWriteTimeout)
	s.hub.Register(conn)
	conn.run(stream.Context())
}
