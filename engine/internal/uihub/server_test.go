package uihub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// minimal fakes so the server test doesn't need real cores.
type doerNoop struct{}

func (doerNoop) Do(exec.Command) exec.CmdAck { return exec.CmdAck{Accepted: true} }

type cfgNoop struct{}

func (cfgNoop) GetConfig(string) (string, bool, error) { return "", false, nil }
func (cfgNoop) SetConfig(string, string)               {}

type indNoop struct{}

func (indNoop) EnsureIndicator(string, md.IndicatorSpec) {}
func (indNoop) ReleaseIndicator(string)                  {}

func TestServerWSSubscribeSnapshot(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	// Build a hub with a mirror via the exported constructor path used by main.
	h, m := uihub.NewHubForTest(clk) // see note: a tiny test constructor exported in server_test-support
	_ = m
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
		uihub.ServerConfig{OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// exec.status always has an assembled snapshot
	sub, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicExecStatus})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatal(err)
	}
	var mm map[string]any
	_ = json.Unmarshal(data, &mm)
	if mm["kind"] != "snapshot" || mm["topic"] != "exec.status" {
		t.Fatalf("expected exec.status snapshot, got %v", mm)
	}
}

type fillsNoop struct{}

func (fillsNoop) QueryFills(string, int64, int64) ([]exec.FillRow, error) { return nil, nil }

func TestServerStaticFileServing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>etape</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)
	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
		uihub.ServerConfig{DistDir: dir, OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("static index should 200, got %d", resp.StatusCode)
	}
	// SPA fallback: an unknown non-file path also returns index.html
	resp2, err := http.Get(ts.URL + "/trading")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("SPA fallback should serve index.html for /trading, got %d", resp2.StatusCode)
	}
}
