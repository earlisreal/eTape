package linearperp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type Stream struct {
	client *Client
	mu     sync.Mutex
	demand map[string]feed.Demand
	events chan feed.Event
	wake   [2]chan struct{}
}

func NewStream(client *Client) *Stream {
	return &Stream{client: client, demand: make(map[string]feed.Demand), events: make(chan feed.Event, 1024),
		wake: [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}}
}

func (s *Stream) Events() <-chan feed.Event { return s.events }

func (s *Stream) Ensure(d feed.Demand) {
	s.mu.Lock()
	s.demand[d.ID] = d
	s.mu.Unlock()
	s.changed()
}

func (s *Stream) Release(id string) {
	s.mu.Lock()
	_, exists := s.demand[id]
	delete(s.demand, id)
	s.mu.Unlock()
	if exists {
		s.changed()
	}
}

func (s *Stream) changed() {
	for _, ch := range s.wake {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type topic struct {
	symbol string
	kind   string
}

const (
	tapeTopic = "trade"
	markTopic = "mark"
	bookTopic = "book"
	barTopic  = "kline"
)

func (s *Stream) wanted(lane int) map[string]topic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]topic)
	for _, d := range s.demand {
		id, ok := instrument.ParsePerpetual(d.Symbol)
		if !ok || id.Exchange != s.client.exchange {
			continue
		}
		for _, sub := range d.Subs {
			add := func(kind, name string) {
				out[name] = topic{symbol: d.Symbol, kind: kind}
			}
			if s.client.exchange == Binance {
				native := strings.ToLower(id.Native)
				if lane == 1 {
					if sub == feed.SubBook {
						add(bookTopic, native+"@depth20@100ms")
					}
					continue
				}
				switch sub {
				case feed.SubTicker:
					add(tapeTopic, native+"@aggTrade")
					add(markTopic, native+"@markPrice@1s")
				case feed.SubQuote:
					add(markTopic, native+"@markPrice@1s")
				case feed.SubKL1m:
					add(barTopic, native+"@kline_1m")
				}
				continue
			}
			switch sub {
			case feed.SubTicker:
				add(tapeTopic, id.Native+"@trade")
				add(markTopic, id.Native+"@markPrice")
			case feed.SubQuote:
				add(markTopic, id.Native+"@markPrice")
			case feed.SubBook:
				add(bookTopic, id.Native+"@depth20")
			case feed.SubKL1m:
				add(barTopic, id.Native+"@kline_1m")
			}
		}
	}
	return out
}

func (s *Stream) Run(ctx context.Context) {
	defer close(s.events)
	var workers sync.WaitGroup
	workers.Go(func() { s.run(ctx, 0, s.client.marketWS) })
	if s.client.bookWS != "" {
		workers.Go(func() { s.run(ctx, 1, s.client.bookWS) })
	}
	workers.Wait()
}

func (s *Stream) run(ctx context.Context, lane int, endpoint string) {
	for ctx.Err() == nil {
		if len(s.wanted(lane)) == 0 {
			select {
			case <-ctx.Done():
			case <-s.wake[lane]:
			}
			continue
		}
		if err := s.session(ctx, lane, endpoint); err != nil && ctx.Err() == nil {
			slog.Debug("perpetual public feed reconnect", "exchange", s.client.exchange, "err", err)
		}
		if len(s.wanted(lane)) > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *Stream) emit(ctx context.Context, ev feed.Event) {
	if ev == nil {
		return
	}
	select {
	case s.events <- ev:
	case <-ctx.Done():
	}
}

func (s *Stream) disconnect(ctx context.Context, t topic) {
	switch t.kind {
	case bookTopic:
		s.emit(ctx, feed.BookEvent{Book: feed.Book{Symbol: t.symbol}})
	case tapeTopic:
		s.emit(ctx, feed.SymbolGapEvent{Symbol: t.symbol})
	}
}

func (s *Stream) session(ctx context.Context, lane int, endpoint string) error {
	dial, cancelDial := context.WithTimeout(ctx, 8*time.Second)
	conn, _, err := websocket.Dial(dial, endpoint, &websocket.DialOptions{HTTPClient: s.client.http})
	cancelDial()
	if err != nil {
		return err
	}
	conn.SetReadLimit(4 << 20)
	socket, cancel := context.WithCancel(ctx)
	inbox := make(chan []byte, 256)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(inbox)
		for {
			read, done := context.WithTimeout(socket, 55*time.Second)
			_, raw, err := conn.Read(read)
			done()
			if err != nil {
				return
			}
			select {
			case inbox <- raw:
			case <-socket.Done():
				return
			}
		}
	}()
	active := map[string]topic{}
	revisions := map[string]int64{}
	last := map[string]float64{}
	defer func() {
		cancel()
		_ = conn.CloseNow()
		<-readerDone
		for _, t := range active {
			s.disconnect(ctx, t)
		}
	}()
	serial := 0
	change := func(subscribe bool, names []string) error {
		if len(names) == 0 {
			return nil
		}
		sort.Strings(names)
		send, done := context.WithTimeout(socket, 4*time.Second)
		defer done()
		if s.client.exchange == Binance {
			method := "UNSUBSCRIBE"
			if subscribe {
				method = "SUBSCRIBE"
			}
			serial++
			return wsjson.Write(send, conn, map[string]any{"method": method, "params": names, "id": serial})
		}
		method := "unsub"
		if subscribe {
			method = "sub"
		}
		for _, name := range names {
			serial++
			if err := wsjson.Write(send, conn, map[string]string{"reqType": method, "dataType": name, "id": fmt.Sprint(serial)}); err != nil {
				return err
			}
		}
		return nil
	}
	refresh := func() (bool, error) {
		wanted := s.wanted(lane)
		var add, remove []string
		for name := range active {
			if _, ok := wanted[name]; !ok {
				remove = append(remove, name)
			}
		}
		for name := range wanted {
			if _, ok := active[name]; !ok {
				add = append(add, name)
			}
		}
		if err := change(false, remove); err != nil {
			return false, err
		}
		for _, name := range remove {
			s.disconnect(ctx, active[name])
			if active[name].kind == tapeTopic {
				delete(last, active[name].symbol)
			}
			delete(active, name)
			delete(revisions, name)
		}
		if len(wanted) == 0 {
			return false, nil
		}
		if err := change(true, add); err != nil {
			return false, err
		}
		for _, name := range add {
			active[name] = wanted[name]
		}
		return true, nil
	}
	if enabled, err := refresh(); err != nil || !enabled {
		return err
	}
	for {
		select {
		case <-socket.Done():
			return socket.Err()
		case <-s.wake[lane]:
			if enabled, err := refresh(); err != nil || !enabled {
				return err
			}
		case raw, ok := <-inbox:
			if !ok {
				return io.EOF
			}
			msg, err := decode(raw)
			if err != nil {
				return err
			}
			if bytes.Equal(msg, []byte("Ping")) {
				reply, done := context.WithTimeout(socket, time.Second)
				err := conn.Write(reply, websocket.MessageText, []byte("Pong"))
				done()
				if err != nil {
					return err
				}
				continue
			}
			var frame struct {
				Stream string          `json:"stream"`
				Topic  string          `json:"dataType"`
				Data   json.RawMessage `json:"data"`
				Time   int64           `json:"ts"`
				Code   int             `json:"code"`
			}
			if err := json.Unmarshal(msg, &frame); err != nil {
				return err
			}
			if frame.Code != 0 {
				return fmt.Errorf("public WebSocket request rejected (code %d)", frame.Code)
			}
			name := frame.Stream
			if s.client.exchange == BingX {
				name = frame.Topic
			}
			if t, tracked := active[name]; tracked {
				event, revision := parseEvent(s.client.exchange, t, frame.Data, frame.Time)
				if revision > 0 {
					if revision <= revisions[name] {
						continue
					}
					revisions[name] = revision
				}
				switch value := event.(type) {
				case feed.TicksEvent:
					last[t.symbol] = value.Ticks[len(value.Ticks)-1].Price
				case feed.QuoteEvent:
					value.Quote.Last = last[t.symbol]
					event = value
				}
				s.emit(ctx, event)
			}
		}
	}
}

func decode(raw []byte) ([]byte, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return raw, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, 4<<20))
}
