package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// fakeSocket is an in-memory wsSocket: reads pop from `in`, writes append to `out`.
type fakeSocket struct {
	in     chan []byte
	mu     sync.Mutex
	out    [][]byte
	closed bool
}

func newFakeSocket() *fakeSocket { return &fakeSocket{in: make(chan []byte, 16)} }
func (s *fakeSocket) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b, ok := <-s.in:
		if !ok {
			return nil, errors.New("closed")
		}
		return b, nil
	}
}
func (s *fakeSocket) Write(ctx context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, append([]byte(nil), b...))
	return nil
}
func (s *fakeSocket) Close(code int, reason string) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
func (s *fakeSocket) writes() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.out...)
}

type fakeCmd struct{ last string }

func (f *fakeCmd) handle(_ context.Context, name string, _ json.RawMessage, _ uint64) wsmsg.AckMsg {
	f.last = name
	return wsmsg.AckMsg{Kind: "ack", Status: "accepted", OrderID: "ET9"}
}

type fakeQuery struct{}

func (fakeQuery) handle(_ string, _ json.RawMessage) any { return []wsmsg.Fill{} }

func TestConnPingPong(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"ping","t":123}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "pong" && m["t"] == float64(123) {
				return true
			}
		}
		return false
	})
}

func TestConnCommandProducesAck(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	cmd := &fakeCmd{}
	c := newConn(1, sock, h, cmd, fakeQuery{}, 8)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"command","corrId":"c1","name":"SubmitOrder","args":{}}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "ack" && m["corrId"] == "c1" && m["status"] == "accepted" && m["orderId"] == "ET9" {
				return true
			}
		}
		return false
	})
	if cmd.last != "SubmitOrder" {
		t.Fatalf("command not dispatched: %q", cmd.last)
	}
}

func TestConnSubscribeRoutesToHub(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8)
	h.Register(c)
	go c.run(ctx)
	sock.in <- []byte(`{"kind":"subscribe","topic":"exec.status"}`)
	h.sync()
	h.sync() // second barrier: subscribe processed after the reader forwards it
	// exec.status snapshot is always available (assembled aggregate) => a frame should be written
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var mm map[string]any
			_ = json.Unmarshal(w, &mm)
			if mm["kind"] == "snapshot" && mm["topic"] == "exec.status" {
				return true
			}
		}
		return false
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
