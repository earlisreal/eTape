package uihubtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Options{Path: "test.db", Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// dialWS opens a client, subscribes to topics, and returns the conn.
func dialWS(t *testing.T, ctx context.Context, url string, topics ...wsmsg.Topic) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range topics {
		b, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: tp})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// waitFrame reads until a frame satisfies pred or the deadline passes.
func waitFrame(t *testing.T, ctx context.Context, c *websocket.Conn, pred func(m map[string]any) bool) map[string]any {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && pred(m) {
			return m
		}
	}
}

// sendCommand writes a command frame and returns its corrId.
func sendCommand(t *testing.T, ctx context.Context, c *websocket.Conn, name string, args map[string]any) string {
	t.Helper()
	corr := "c-" + name
	raw, _ := json.Marshal(args)
	b, _ := json.Marshal(wsmsg.CommandMsg{Kind: "command", CorrID: corr, Name: name, Args: raw})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return corr
}

// forwardMD relays md.Core.Updates() to the hub.
func forwardMD(ctx context.Context, mdCore *md.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-mdCore.Updates():
			hub.PublishMD(u)
		}
	}
}

// forwardExec relays exec.Core.Updates() to the hub.
func forwardExec(ctx context.Context, execCore *exec.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-execCore.Updates():
			hub.PublishExec(u)
		}
	}
}

// deterministicReader returns a fixed PRNG reader for replay determinism.
func deterministicReader() *strings.Reader {
	return strings.NewReader(strings.Repeat("etape-seed-0123456789", 64))
}
