package mexc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

// Stream coalesces panel demand by contract onto the public Futures socket.
type Stream struct {
	client *Client
	events chan feed.Event
	wake   chan struct{}
	mu     sync.Mutex
	demand map[string]feed.Demand
}

func NewStream(client *Client) *Stream {
	return &Stream{client: client, events: make(chan feed.Event, 1024), wake: make(chan struct{}, 1), demand: make(map[string]feed.Demand)}
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
	delete(s.demand, id)
	s.mu.Unlock()
	s.changed()
}

func (s *Stream) changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type subscription struct {
	native  string
	channel string
}

func (s *Stream) wanted() map[subscription]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := make(map[subscription]bool)
	for _, d := range s.demand {
		id, ok := parseSymbol(d.Symbol)
		if !ok {
			continue
		}
		for _, sub := range d.Subs {
			switch sub {
			case feed.SubTicker:
				wanted[subscription{id.Native, "deal"}] = true
				wanted[subscription{id.Native, "ticker"}] = true
			case feed.SubQuote:
				wanted[subscription{id.Native, "ticker"}] = true
			case feed.SubBook:
				wanted[subscription{id.Native, "depth"}] = true
			case feed.SubKL1m:
				wanted[subscription{id.Native, "kline"}] = true
			}
		}
	}
	return wanted
}

func (s *Stream) emit(ctx context.Context, event feed.Event) {
	select {
	case s.events <- event:
	case <-ctx.Done():
	}
}

func (s *Stream) Run(ctx context.Context) {
	defer close(s.events)
	for ctx.Err() == nil {
		if len(s.wanted()) == 0 {
			select {
			case <-ctx.Done():
			case <-s.wake:
			}
			continue
		}
		s.runSession(ctx)
		if len(s.wanted()) > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

type message struct {
	Channel string          `json:"channel"`
	Symbol  string          `json:"symbol"`
	Data    json.RawMessage `json:"data"`
}

type request struct {
	Method   string            `json:"method"`
	Param    map[string]string `json:"param,omitempty"`
	Compress *bool             `json:"compress,omitempty"`
}

func (s *Stream) runSession(ctx context.Context) {
	dial, stopDial := context.WithTimeout(ctx, 8*time.Second)
	conn, _, err := websocket.Dial(dial, s.client.stream, &websocket.DialOptions{HTTPClient: s.client.http})
	stopDial()
	if err != nil {
		slog.Debug("MEXC perpetual stream connection", "err", err)
		return
	}
	conn.SetReadLimit(4 << 20)
	socketCtx, cancel := context.WithCancel(ctx)
	readerDone := make(chan struct{})
	inbox := make(chan message, 256)
	go func() {
		defer close(readerDone)
		defer close(inbox)
		for {
			var m message
			read, stop := context.WithTimeout(socketCtx, 40*time.Second)
			err := wsjson.Read(read, conn, &m)
			stop()
			if err != nil {
				return
			}
			select {
			case inbox <- m:
			case <-socketCtx.Done():
				return
			}
		}
	}()
	active := make(map[subscription]bool)
	books := make(map[string]*orderBook)
	defer func() {
		cancel()
		_ = conn.CloseNow()
		<-readerDone
		for sub := range active {
			if sub.channel == "depth" {
				s.clearBook(ctx, sub.native)
			}
			if sub.channel == "deal" {
				s.emit(ctx, feed.SymbolGapEvent{Symbol: instrumentSymbol(sub.native)})
			}
		}
	}()
	write := func(r request) error {
		send, stop := context.WithTimeout(socketCtx, 3*time.Second)
		defer stop()
		return wsjson.Write(send, conn, r)
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		wanted := s.wanted()
		for sub := range active {
			if !wanted[sub] {
				if write(subscriptionRequest(sub, false)) != nil {
					return
				}
				delete(active, sub)
				if sub.channel == "depth" {
					delete(books, sub.native)
					s.clearBook(ctx, sub.native)
				}
			}
		}
		if len(wanted) == 0 {
			return
		}
		for sub := range wanted {
			if !active[sub] {
				if write(subscriptionRequest(sub, true)) != nil {
					return
				}
				active[sub] = true
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-heartbeat.C:
			if write(request{Method: "ping"}) != nil {
				return
			}
		case m, ok := <-inbox:
			if !ok {
				return
			}
			if err := s.handle(ctx, active, books, m); err != nil {
				slog.Debug("MEXC perpetual stream recovery", "channel", m.Channel, "symbol", m.Symbol, "err", err)
				return
			}
		}
	}
}

func subscriptionRequest(sub subscription, start bool) request {
	verb := "unsub."
	if start {
		verb = "sub."
	}
	r := request{Method: verb + sub.channel, Param: map[string]string{"symbol": sub.native}}
	if sub.channel == "kline" {
		r.Param["interval"] = "Min1"
	}
	if start && (sub.channel == "deal" || sub.channel == "depth") {
		disabled := false
		r.Compress = &disabled
	}
	return r
}

func instrumentSymbol(native string) string {
	return (instrument.Perpetual{Exchange: Exchange, Native: native}).String()
}

func (s *Stream) clearBook(ctx context.Context, native string) {
	s.emit(ctx, feed.BookEvent{Book: feed.Book{Symbol: instrumentSymbol(native), TsMs: time.Now().UnixMilli()}})
}

type trade struct {
	Price  float64     `json:"p"`
	Volume json.Number `json:"v"`
	Side   int         `json:"T"`
	Time   int64       `json:"t"`
	ID     json.Number `json:"i"`
}

type candle struct {
	Time     int64       `json:"t"`
	Open     float64     `json:"o"`
	High     float64     `json:"h"`
	Low      float64     `json:"l"`
	Close    float64     `json:"c"`
	Volume   json.Number `json:"q"`
	Turnover float64     `json:"a"`
}

type ticker struct {
	Last     float64     `json:"lastPrice"`
	Mark     float64     `json:"fairPrice"`
	Index    float64     `json:"indexPrice"`
	Funding  float64     `json:"fundingRate"`
	Volume   json.Number `json:"volume24"`
	Turnover float64     `json:"amount24"`
	High     float64     `json:"high24Price"`
	Low      float64     `json:"lower24Price"`
	Time     int64       `json:"timestamp"`
}

func (s *Stream) handle(ctx context.Context, active map[subscription]bool, books map[string]*orderBook, m message) error {
	if m.Symbol == "" {
		return nil
	}
	symbol := instrumentSymbol(m.Symbol)
	switch m.Channel {
	case "push.deal":
		if !active[subscription{m.Symbol, "deal"}] {
			break
		}
		var batch []trade
		if err := json.Unmarshal(m.Data, &batch); err != nil {
			return err
		}
		ticks := make([]feed.Tick, 0, len(batch))
		receipt := time.Now().UnixMilli()
		for _, t := range batch {
			qty, ok := contracts(t.Volume)
			id, err := t.ID.Int64()
			if !ok || err != nil || qty == 0 || id <= 0 || t.Time <= 0 || t.Price <= 0 {
				continue
			}
			side := feed.Neutral
			switch t.Side {
			case 1:
				side = feed.Buy
			case 2:
				side = feed.Sell
			}
			ticks = append(ticks, feed.Tick{Symbol: symbol, Price: t.Price, Volume: qty, Seq: id, Dir: side,
				TsMs: t.Time, RecvTsMs: receipt, Delivery: feed.DeliveryRealtime})
		}
		sort.SliceStable(ticks, func(i, j int) bool { return ticks[i].TsMs < ticks[j].TsMs })
		if len(ticks) > 0 {
			s.emit(ctx, feed.TicksEvent{Ticks: ticks})
		}
	case "push.kline":
		if !active[subscription{m.Symbol, "kline"}] {
			break
		}
		var c candle
		if err := json.Unmarshal(m.Data, &c); err != nil {
			return err
		}
		if qty, ok := contracts(c.Volume); ok && c.Time > 0 {
			bar := feed.Bar{Symbol: symbol, BucketMs: c.Time * 1_000, O: c.Open, H: c.High, L: c.Low, C: c.Close, Volume: qty, Turnover: c.Turnover}
			s.emit(ctx, feed.Bars1mEvent{Bars: []feed.Bar{bar}})
		}
	case "push.ticker":
		if !active[subscription{m.Symbol, "ticker"}] {
			break
		}
		var t ticker
		if err := json.Unmarshal(m.Data, &t); err != nil {
			return err
		}
		if qty, ok := contracts(t.Volume); ok && t.Time > 0 {
			s.emit(ctx, feed.QuoteEvent{Quote: feed.Quote{Symbol: symbol, TsMs: t.Time, Last: t.Last, High: t.High, Low: t.Low,
				Volume: qty, Turnover: t.Turnover, Mark: t.Mark, Index: t.Index, FundingRate: t.Funding}})
		}
	case "push.depth":
		if !active[subscription{m.Symbol, "depth"}] {
			break
		}
		var delta depth
		if err := json.Unmarshal(m.Data, &delta); err != nil {
			return err
		}
		book := books[m.Symbol]
		if book != nil && time.Now().Before(book.retryAt) {
			break
		}
		if book == nil || (delta.Version > book.version && delta.Begin != book.version+1) {
			s.clearBook(ctx, m.Symbol)
			var err error
			book, err = s.client.recoverBook(ctx, m.Symbol, delta)
			if err != nil {
				slog.Debug("MEXC perpetual book pending", "symbol", symbol, "err", err)
				books[m.Symbol] = &orderBook{retryAt: time.Now().Add(time.Second)}
				break
			}
			books[m.Symbol] = book
		}
		if delta.Version > book.version {
			if delta.Begin != book.version+1 || delta.End != delta.Version {
				return errors.New("MEXC depth update skipped a version")
			}
			if err := book.apply(delta); err != nil {
				return err
			}
		}
		s.emit(ctx, feed.BookEvent{Book: book.snapshot(symbol)})
	}
	return nil
}

func (c *Client) recoverBook(ctx context.Context, native string, delta depth) (*orderBook, error) {
	var snapshot depth
	if err := c.get(ctx, "/api/v1/contract/depth/"+native+"?limit=1000", &snapshot); err != nil {
		return nil, err
	}
	book := &orderBook{bids: make(map[float64]feed.BookLevel), asks: make(map[float64]feed.BookLevel)}
	if err := book.apply(snapshot); err != nil {
		return nil, err
	}
	if delta.Version <= book.version || delta.Begin == book.version+1 {
		return book, nil
	}
	var commits []depth
	if err := c.get(ctx, "/api/v1/contract/depth_commits/"+native+"/1000", &commits); err != nil {
		return nil, err
	}
	sort.Slice(commits, func(i, j int) bool { return commits[i].Version < commits[j].Version })
	for _, commit := range commits {
		if commit.Version <= book.version {
			continue
		}
		if commit.Version != book.version+1 {
			break
		}
		if err := book.apply(commit); err != nil {
			return nil, err
		}
	}
	if delta.Version > book.version && delta.Begin != book.version+1 {
		return nil, fmt.Errorf("MEXC depth version %d cannot bridge to update %d–%d", book.version, delta.Begin, delta.Version)
	}
	return book, nil
}

type depth struct {
	Asks      [][]json.Number `json:"asks"`
	Bids      [][]json.Number `json:"bids"`
	Version   int64           `json:"version"`
	Begin     int64           `json:"begin"`
	End       int64           `json:"end"`
	TradeTime json.Number     `json:"cts"`
	Timestamp int64           `json:"timestamp"`
}

type orderBook struct {
	bids, asks map[float64]feed.BookLevel
	version    int64
	ts         int64
	retryAt    time.Time
}

func (b *orderBook) apply(d depth) error {
	if d.Version <= 0 {
		return errors.New("missing MEXC depth version")
	}
	if err := applyLevels(b.bids, d.Bids); err != nil {
		return err
	}
	if err := applyLevels(b.asks, d.Asks); err != nil {
		return err
	}
	b.version = d.Version
	b.ts, _ = d.TradeTime.Int64()
	if b.ts <= 0 {
		b.ts = d.Timestamp
	}
	return nil
}

func applyLevels(levels map[float64]feed.BookLevel, updates [][]json.Number) error {
	for _, row := range updates {
		if len(row) < 2 {
			return errors.New("short MEXC depth row")
		}
		price, err := row[0].Float64()
		qty, ok := contracts(row[1])
		if err != nil || !ok || price <= 0 || math.IsInf(price, 0) || math.IsNaN(price) {
			return errors.New("invalid MEXC depth level")
		}
		if qty == 0 {
			delete(levels, price)
		} else {
			levels[price] = feed.BookLevel{Price: price, Volume: qty}
		}
	}
	return nil
}

func (b *orderBook) snapshot(symbol string) feed.Book {
	return feed.Book{Symbol: symbol, TsMs: b.ts, Bids: bestLevels(b.bids, true), Asks: bestLevels(b.asks, false)}
}

func bestLevels(levels map[float64]feed.BookLevel, bids bool) []feed.BookLevel {
	sorted := make([]feed.BookLevel, 0, len(levels))
	for _, level := range levels {
		sorted = append(sorted, level)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if bids {
			return sorted[i].Price > sorted[j].Price
		}
		return sorted[i].Price < sorted[j].Price
	})
	return sorted[:min(len(sorted), 20)]
}
