package uihub

import (
	"context"
	"encoding/json"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// client is the hub's view of a connected UI socket (implemented by *conn, Task 7).
type client interface {
	id() uint64
	enqueue(b []byte) bool // false => outbound queue full; hub closes+drops the client
	close()
}

type HubConfig struct {
	MDInterval       time.Duration
	AccountInterval  time.Duration
	PositionInterval time.Duration
	Buf              int // channel buffer depth for md/exec/pub inbound
}

type subReq struct {
	c     client
	topic wsmsg.Topic
}

type pub struct {
	topic   wsmsg.Topic
	key     string
	payload any
}

// Hub is a single-goroutine event loop that owns the mirror, the connected-
// client set, and per-topic-class coalescing buffers. Every field below the
// channel declarations is touched only from within Run's goroutine; all other
// goroutines communicate with the hub exclusively via the channels, which is
// what makes the single-writer discipline verifiable with go test -race.
type Hub struct {
	clk clock.Clock
	cfg HubConfig
	m   *mirror

	register   chan client
	unregister chan client
	subCh      chan subReq
	unsubCh    chan subReq
	mdCh       chan md.Update
	execCh     chan exec.Update
	pubCh      chan pub
	syncCh     chan chan struct{} // test barrier

	// Run-loop-owned:
	clients   map[client]map[wsmsg.Topic]bool
	pendKeep  map[string]staged       // classMDKeep, flushed on md ticker
	tapePend  map[string][]wsmsg.Tick // symbol -> accumulated ticks
	acctPend  map[string]staged       // venue -> latest account frame
	posLatest staged
	posDirty  bool
}

func NewHub(clk clock.Clock, cfg HubConfig, m *mirror) *Hub {
	if cfg.Buf <= 0 {
		cfg.Buf = 1024
	}
	return &Hub{
		clk: clk, cfg: cfg, m: m,
		register:   make(chan client),
		unregister: make(chan client),
		subCh:      make(chan subReq),
		unsubCh:    make(chan subReq),
		mdCh:       make(chan md.Update, cfg.Buf),
		execCh:     make(chan exec.Update, cfg.Buf),
		pubCh:      make(chan pub, cfg.Buf),
		syncCh:     make(chan chan struct{}),
		clients:    map[client]map[wsmsg.Topic]bool{},
		pendKeep:   map[string]staged{},
		tapePend:   map[string][]wsmsg.Tick{},
		acctPend:   map[string]staged{},
	}
}

// Public entry points (safe from any goroutine; they only send on channels).
func (h *Hub) Register(c client)                        { h.register <- c }
func (h *Hub) Unregister(c client)                      { h.unregister <- c }
func (h *Hub) Subscribe(c client, t wsmsg.Topic)        { h.subCh <- subReq{c, t} }
func (h *Hub) Unsubscribe(c client, t wsmsg.Topic)      { h.unsubCh <- subReq{c, t} }
func (h *Hub) PublishMD(u md.Update)                    { h.mdCh <- u }
func (h *Hub) PublishExec(u exec.Update)                { h.execCh <- u }
func (h *Hub) Publish(t wsmsg.Topic, key string, p any) { h.pubCh <- pub{t, key, p} }

// sync is a test-only synchronous barrier: it blocks until the Run loop has
// drained and processed every message sent on the hub's channels before this
// call. It is unexported and used only by hub_test.go's syncHub helper.
func (h *Hub) sync() { done := make(chan struct{}); h.syncCh <- done; <-done }

func (h *Hub) Run(ctx context.Context) error {
	mdTick := h.clk.NewTicker(h.cfg.MDInterval)
	acctTick := h.clk.NewTicker(h.cfg.AccountInterval)
	posTick := h.clk.NewTicker(h.cfg.PositionInterval)
	defer mdTick.Stop()
	defer acctTick.Stop()
	defer posTick.Stop()

	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.close()
			}
			return ctx.Err()
		case c := <-h.register:
			h.clients[c] = map[wsmsg.Topic]bool{}
		case c := <-h.unregister:
			delete(h.clients, c)
			c.close()
		case r := <-h.subCh:
			if subs, ok := h.clients[r.c]; ok {
				subs[r.topic] = true
				h.sendSnapshot(r.c, r.topic)
			}
		case r := <-h.unsubCh:
			if subs, ok := h.clients[r.c]; ok {
				delete(subs, r.topic)
			}
		case u := <-h.mdCh:
			for _, s := range h.m.applyMD(u) {
				h.stageMD(s)
			}
		case u := <-h.execCh:
			for _, s := range h.m.applyExec(u) {
				h.stageExec(s)
			}
		case p := <-h.pubCh:
			s := staged{Topic: p.topic, Key: p.key, Payload: p.payload}
			h.m.applyPub(s)
			h.broadcast(s, false)
		case <-mdTick.C():
			h.flushMD()
		case <-acctTick.C():
			h.flushAcct()
		case <-posTick.C():
			if h.posDirty {
				h.broadcast(h.posLatest, false)
				h.posDirty = false
			}
		case done := <-h.syncCh:
			close(done)
		}
	}
}

func (h *Hub) stageMD(s staged) {
	switch classify(s.Topic) {
	case classTape:
		ticks, _ := s.Payload.([]wsmsg.Tick)
		sym := ""
		if len(ticks) > 0 {
			sym = ticks[0].Symbol
		}
		h.tapePend[sym] = append(h.tapePend[sym], ticks...)
	case classMDKeep:
		h.pendKeep[dedupOf(s)] = s
	default: // indicator: immediate; Snap decides snapshot vs delta
		h.broadcast(s, s.Snap)
	}
}

func (h *Hub) stageExec(s staged) {
	switch classify(s.Topic) {
	case classAccount:
		h.acctPend[dedupOf(s)] = s
	case classPositions:
		h.posLatest = s
		h.posDirty = true
	default: // orders, fills, status
		h.broadcast(s, false)
	}
}

func (h *Hub) flushMD() {
	for k, s := range h.pendKeep {
		h.broadcast(s, false)
		delete(h.pendKeep, k)
	}
	for sym, ticks := range h.tapePend {
		if len(ticks) == 0 {
			continue
		}
		h.broadcast(staged{Topic: wsmsg.TopicTape, Payload: ticks}, false)
		delete(h.tapePend, sym)
	}
}

func (h *Hub) flushAcct() {
	for k, s := range h.acctPend {
		h.broadcast(s, false)
		delete(h.acctPend, k)
	}
}

func (h *Hub) broadcast(s staged, snap bool) {
	var b []byte
	var err error
	if snap {
		b, err = json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	} else {
		b, err = json.Marshal(wsmsg.DeltaMsg{Kind: "delta", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	}
	if err != nil {
		return
	}
	var dead []client
	for c, subs := range h.clients {
		if subs[s.Topic] {
			if !c.enqueue(b) {
				dead = append(dead, c)
			}
		}
	}
	for _, c := range dead {
		delete(h.clients, c)
		c.close()
	}
}

func (h *Hub) sendSnapshot(c client, topic wsmsg.Topic) {
	for _, fr := range h.m.snapshotFrames(topic) {
		b, err := json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: fr.Topic, Key: fr.Key, Payload: fr.Payload})
		if err != nil {
			continue
		}
		if !c.enqueue(b) {
			delete(h.clients, c)
			c.close()
			return
		}
	}
}
