package uihub

import (
	"maps"
	"slices"
	"sort"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// staged is one delta/snapshot frame the hub will broadcast. Snap is true only
// for a mid-stream full-series indicator update (broadcast as kind:"snapshot");
// every other staged frame is a delta.
type staged struct {
	Topic   wsmsg.Topic
	Key     string
	Payload any
	Snap    bool
	Batch   bool // a bars batch-prepend delta: broadcast immediately, lossless, never coalesced
}

// venueMeta is the static per-venue config the mirror needs to assemble exec.status.
type venueMeta struct {
	ID     string
	Broker wsmsg.Broker
	Note   string
	Gate   wsmsg.GateLimitsView
}

type mirror struct {
	global wsmsg.GlobalLimitsView

	// market data (keyed by symbol unless noted)
	quotes     map[string]wsmsg.Quote
	books      map[string]wsmsg.Book
	tape       map[string][]wsmsg.Tick           // bounded recent ring per symbol
	bars       map[string][]wsmsg.Bar            // key "SYMBOL:TF", sorted by bucketStart
	indicators map[string][]wsmsg.IndicatorPoint // key instanceId or instanceId#slot
	marks      map[string]float64                // last price per symbol (pnl + display)

	// scanner / news
	rank         map[string]wsmsg.ScannerRankPayload // key session
	detail       map[string]wsmsg.StockDetailPayload // key symbol
	news         []wsmsg.NewsItem                    // bounded recent
	watchlist    wsmsg.WatchlistRowsPayload          // the one global list snapshot
	watchlistSet bool                                // false until the first publish

	// execution
	accounts    map[string]wsmsg.AccountRow // key venue
	positions   map[string]exec.Position    // key "venue|symbol"; mapped w/ mark on read
	orders      map[string]wsmsg.Order      // key orderID
	fills       []wsmsg.Fill                // bounded recent
	trades      []wsmsg.ClosedTradeRow      // bounded recent
	venueStatus map[string]*wsmsg.VenueStatus
	masterArmed bool

	// system
	health  wsmsg.HealthSnapshot
	session wsmsg.SessionSnapshot
	boot    wsmsg.BootStatus
	events  []wsmsg.SysEvent // bounded recent

	tapeCap, newsCap, fillsCap, eventsCap, tradesCap int
	venueOrder                                       []string // stable venue order for exec.status
}

func newMirror(venues []venueMeta, global wsmsg.GlobalLimitsView, tapeCap, newsCap, fillsCap, eventsCap, tradesCap int) *mirror {
	m := &mirror{
		global:      global,
		quotes:      map[string]wsmsg.Quote{},
		books:       map[string]wsmsg.Book{},
		tape:        map[string][]wsmsg.Tick{},
		bars:        map[string][]wsmsg.Bar{},
		indicators:  map[string][]wsmsg.IndicatorPoint{},
		marks:       map[string]float64{},
		rank:        map[string]wsmsg.ScannerRankPayload{},
		detail:      map[string]wsmsg.StockDetailPayload{},
		accounts:    map[string]wsmsg.AccountRow{},
		positions:   map[string]exec.Position{},
		orders:      map[string]wsmsg.Order{},
		venueStatus: map[string]*wsmsg.VenueStatus{},
		tapeCap:     tapeCap, newsCap: newsCap, fillsCap: fillsCap, eventsCap: eventsCap,
		tradesCap: tradesCap,
	}
	for _, v := range venues {
		m.venueStatus[v.ID] = &wsmsg.VenueStatus{
			Venue: v.ID, Broker: v.Broker, Gate: v.Gate,
			Note: v.Note,
		}
		m.venueOrder = append(m.venueOrder, v.ID)
	}
	return m
}

func barKey(symbol, tf string) string { return symbol + ":" + tf }

// applyMD updates market-data state and returns delta frames to broadcast.
func (m *mirror) applyMD(u md.Update) []staged {
	switch v := u.(type) {
	case md.QuoteUpdate:
		m.marks[v.Quote.Symbol] = v.Quote.Last
		bid, ask := m.topOfBook(v.Quote.Symbol)
		q := mapQuote(v.Quote, bid, ask)
		m.quotes[v.Quote.Symbol] = q
		return []staged{{Topic: wsmsg.TopicQuote, Payload: q}}
	case md.BookUpdate:
		b := mapBook(v.Book)
		m.books[v.Book.Symbol] = b
		// keep the cached quote's bid/ask fresh (no separate quote delta emitted)
		if q, ok := m.quotes[v.Book.Symbol]; ok {
			q.Bid, q.Ask = m.topOfBook(v.Book.Symbol)
			m.quotes[v.Book.Symbol] = q
		}
		return []staged{{Topic: wsmsg.TopicBook, Payload: b}}
	case md.TapeUpdate:
		out := make([]wsmsg.Tick, 0, len(v.Ticks))
		for _, t := range v.Ticks {
			wt := mapTick(t)
			out = append(out, wt)
			m.marks[t.Symbol] = t.Price
		}
		m.appendTape(v.Symbol, out)
		if len(out) == 0 {
			return nil
		}
		return []staged{{Topic: wsmsg.TopicTape, Payload: out}}
	case md.BarUpdate:
		wb := mapBar(v.Bar)
		m.upsertBar(wb)
		m.marks[wb.Symbol] = wb.C
		return []staged{{Topic: wsmsg.TopicBars, Payload: wb}}
	case md.BarSnapshot:
		// The lossless replacement for a history seed's per-bar BarUpdates
		// (see md.BarSnapshot's doc comment): replace the whole series in one
		// shot instead of len(v.Bars) individual O(n) upsertBar scans, and
		// broadcast it as a snapshot (see Hub.stageMD's Snap fast-path) so it
		// can never be coalesced away like a keep-latest bars delta.
		if len(v.Bars) == 0 {
			return nil
		}
		out := make([]wsmsg.Bar, len(v.Bars))
		for i, b := range v.Bars {
			out[i] = mapBar(b)
		}
		m.bars[barKey(v.Symbol, string(v.TF))] = out
		m.marks[v.Symbol] = out[len(out)-1].C
		return []staged{{Topic: wsmsg.TopicBars, Payload: out, Snap: true}}
	case md.BarPrepend:
		// SeedOlder1m's pan-triggered deepening chunk: bars are ascending and
		// strictly older than the cached run's head, so front-insert rather than
		// upsertBar's scan-and-append (which assumes newer-or-equal bars).
		if len(v.Bars) == 0 {
			return nil
		}
		out := make([]wsmsg.Bar, len(v.Bars))
		for i, b := range v.Bars {
			out[i] = mapBar(b)
		}
		k := barKey(v.Symbol, string(v.TF))
		// out is ascending and strictly older than the cached run's head.
		m.bars[k] = append(out, m.bars[k]...)
		return []staged{{Topic: wsmsg.TopicBars, Payload: out, Batch: true}}
	case md.IndicatorUpdate:
		return m.applyIndicator(v)
	default:
		return nil // MismatchUpdate/ConnUpdate/ResyncedUpdate are handled by main->sys.events, not topics
	}
}

func (m *mirror) topOfBook(symbol string) (bid, ask float64) {
	b, ok := m.books[symbol]
	if !ok {
		return 0, 0
	}
	if len(b.Bids) > 0 {
		bid = b.Bids[0].Price
	}
	if len(b.Asks) > 0 {
		ask = b.Asks[0].Price
	}
	return bid, ask
}

func (m *mirror) appendTape(symbol string, ticks []wsmsg.Tick) {
	r := append(m.tape[symbol], ticks...)
	if len(r) > m.tapeCap {
		r = r[len(r)-m.tapeCap:]
	}
	m.tape[symbol] = r
}

func (m *mirror) upsertBar(b wsmsg.Bar) {
	k := barKey(b.Symbol, b.Timeframe)
	series := m.bars[k]
	for i := range series {
		if series[i].BucketStart == b.BucketStart {
			series[i] = b
			m.bars[k] = series
			return
		}
	}
	series = append(series, b)
	sort.Slice(series, func(i, j int) bool { return series[i].BucketStart < series[j].BucketStart })
	m.bars[k] = series
}

func (m *mirror) applyIndicator(v md.IndicatorUpdate) []staged {
	key := v.SeriesKey
	if v.Snapshot {
		pts := make([]wsmsg.IndicatorPoint, len(v.Points))
		for i, p := range v.Points {
			pts[i] = mapIndicatorPoint(p)
		}
		m.indicators[key] = pts
		return []staged{{Topic: wsmsg.TopicIndicator, Key: key, Payload: pts, Snap: true}}
	}
	if len(v.Points) == 0 {
		return nil
	}
	p := mapIndicatorPoint(v.Points[len(v.Points)-1])
	m.indicators[key] = append(m.indicators[key], p)
	return []staged{{Topic: wsmsg.TopicIndicator, Key: key, Payload: p}}
}

// applyExec updates execution state and returns delta frames to broadcast.
func (m *mirror) applyExec(u exec.Update) []staged {
	switch v := u.(type) {
	case exec.OrderUpdate:
		w := mapOrder(v.Order)
		m.orders[w.ID] = w
		return []staged{{Topic: wsmsg.TopicExecOrders, Payload: w}}
	case exec.FillUpdate:
		w := mapFill(v.Fill)
		m.fills = append(m.fills, w)
		if len(m.fills) > m.fillsCap {
			m.fills = m.fills[len(m.fills)-m.fillsCap:]
		}
		return []staged{{Topic: wsmsg.TopicExecFills, Payload: w}}
	case exec.TradeUpdate:
		w := mapClosedTrade(v.Trade)
		m.trades = append(m.trades, w)
		if len(m.trades) > m.tradesCap {
			m.trades = m.trades[len(m.trades)-m.tradesCap:]
		}
		return []staged{{Topic: wsmsg.TopicExecTrades, Payload: w}}
	case exec.PositionUpdate:
		m.positions[string(v.Position.Venue)+"|"+v.Position.Symbol] = v.Position
		return []staged{{Topic: wsmsg.TopicExecPositions, Payload: m.positionsPayload()}}
	case exec.AccountUpdate:
		a := mapAccount(v.Account)
		m.accounts[a.Venue] = a
		m.masterArmed = v.MasterArmed
		return []staged{
			{Topic: wsmsg.TopicExecAccount, Key: a.Venue, Payload: a},
			{Topic: wsmsg.TopicExecStatus, Payload: m.execStatus()},
		}
	case exec.StatusUpdate:
		m.masterArmed = v.MasterArmed
		if vs := m.venueStatus[string(v.Venue)]; vs != nil {
			vs.Connected = v.Connected
			// Non-empty guard only: unrelated StatusUpdates (arm/kill emitters,
			// BrokerConnUp) carry an empty Note and must never clear a real one.
			// Deliberate consequence: there is no always-overwrite/clear path, so
			// a disconnect note SURVIVES the next reconnect (Connected flips back
			// to true, Note is untouched, now stale). Consumers must gate note
			// display on Connected == false, never show it while connected.
			if v.Note != "" {
				vs.Note = v.Note
			}
		}
		return []staged{{Topic: wsmsg.TopicExecStatus, Payload: m.execStatus()}}
	default:
		return nil
	}
}

func (m *mirror) positionsPayload() []wsmsg.PositionRow {
	rows := make([]wsmsg.PositionRow, 0, len(m.positions))
	for _, k := range sortedKeysOf(m.positions) {
		p := m.positions[k]
		rows = append(rows, mapPosition(p, m.marks[p.Symbol]))
	}
	return rows
}

func (m *mirror) execStatus() wsmsg.ExecStatus {
	vs := make([]wsmsg.VenueStatus, 0, len(m.venueOrder))
	for _, id := range m.venueOrder {
		if s := m.venueStatus[id]; s != nil {
			vs = append(vs, *s)
		}
	}
	return wsmsg.ExecStatus{MasterArmed: m.masterArmed, Global: m.global, Venues: vs}
}

// applyPub records a poller/health/sys event into the mirror (so late subscribers
// get a snapshot). Broadcast of these is event-driven, done by the hub directly.
func (m *mirror) applyPub(s staged) {
	switch s.Topic {
	case wsmsg.TopicScannerRank:
		m.rank[s.Key] = s.Payload.(wsmsg.ScannerRankPayload)
	case wsmsg.TopicStockDetail:
		m.detail[s.Key] = s.Payload.(wsmsg.StockDetailPayload)
	case wsmsg.TopicWatchlistRows:
		m.watchlist = s.Payload.(wsmsg.WatchlistRowsPayload)
		m.watchlistSet = true
	case wsmsg.TopicNews:
		switch p := s.Payload.(type) {
		case wsmsg.NewsItem:
			m.appendNews(p)
		case []wsmsg.NewsItem:
			for _, it := range p {
				m.appendNews(it)
			}
		}
	case wsmsg.TopicSysHealth:
		m.health = s.Payload.(wsmsg.HealthSnapshot)
	case wsmsg.TopicSysBoot:
		m.boot = s.Payload.(wsmsg.BootStatus)
	case wsmsg.TopicSysEvents:
		switch p := s.Payload.(type) {
		case wsmsg.SysEvent:
			m.appendEvent(p)
		case []wsmsg.SysEvent:
			for _, it := range p {
				m.appendEvent(it)
			}
		}
	}
	// scanner.hit and config are not mirrored (hit is transient; config is command-served).
}

func (m *mirror) appendNews(it wsmsg.NewsItem) {
	m.news = append(m.news, it)
	if len(m.news) > m.newsCap {
		m.news = m.news[len(m.news)-m.newsCap:]
	}
}

func (m *mirror) appendEvent(e wsmsg.SysEvent) {
	m.events = append(m.events, e)
	if len(m.events) > m.eventsCap {
		m.events = m.events[len(m.events)-m.eventsCap:]
	}
}

// snapshotFrames serializes current state for a new subscriber to `topic`.
func (m *mirror) snapshotFrames(topic wsmsg.Topic) []staged {
	var out []staged
	switch topic {
	case wsmsg.TopicQuote:
		for _, s := range sortedKeysOf(m.quotes) {
			out = append(out, staged{Topic: topic, Payload: m.quotes[s]})
		}
	case wsmsg.TopicBook:
		for _, s := range sortedKeysOf(m.books) {
			out = append(out, staged{Topic: topic, Payload: m.books[s]})
		}
	case wsmsg.TopicTape:
		// make (not append-to-nil) so an empty tape batch marshals to `[]`,
		// not `null` -- registry.ts:65 calls .length on the payload.
		for _, s := range sortedKeysOf(m.tape) {
			ticks := make([]wsmsg.Tick, 0, len(m.tape[s]))
			out = append(out, staged{Topic: topic, Payload: append(ticks, m.tape[s]...)})
		}
	case wsmsg.TopicBars:
		// make (not append-to-nil) so an empty bars series marshals to `[]`,
		// not `null` -- BarStore.apply calls .slice() on the payload.
		for _, k := range sortedKeysOf(m.bars) {
			bars := make([]wsmsg.Bar, 0, len(m.bars[k]))
			out = append(out, staged{Topic: topic, Payload: append(bars, m.bars[k]...)})
		}
	case wsmsg.TopicIndicator:
		// make (not append-to-nil) so an empty indicator series marshals to
		// `[]`, not `null` -- IndicatorStore.apply calls .slice() on the payload.
		for _, k := range sortedKeysOf(m.indicators) {
			pts := make([]wsmsg.IndicatorPoint, 0, len(m.indicators[k]))
			out = append(out, staged{Topic: topic, Key: k, Payload: append(pts, m.indicators[k]...)})
		}
	case wsmsg.TopicScannerRank:
		for _, sess := range sortedKeysOf(m.rank) {
			out = append(out, staged{Topic: topic, Key: sess, Payload: m.rank[sess]})
		}
	case wsmsg.TopicStockDetail:
		for _, sym := range sortedKeysOf(m.detail) {
			out = append(out, staged{Topic: topic, Key: sym, Payload: m.detail[sym]})
		}
	case wsmsg.TopicWatchlistRows:
		if m.watchlistSet {
			pl := m.watchlist
			if pl.Symbols == nil {
				pl.Symbols = []string{}
			}
			if pl.Rows == nil {
				pl.Rows = []wsmsg.WatchlistRow{}
			}
			out = append(out, staged{Topic: topic, Payload: pl})
		}
	case wsmsg.TopicNews:
		// make (not append-to-nil) so an empty news list marshals to `[]`, not
		// `null` — a null payload crashes the UI NewsStore's dedup.
		news := make([]wsmsg.NewsItem, 0, len(m.news))
		out = append(out, staged{Topic: topic, Payload: append(news, m.news...)})
	case wsmsg.TopicExecAccount:
		for _, v := range m.venueOrder {
			if a, ok := m.accounts[v]; ok {
				out = append(out, staged{Topic: topic, Key: v, Payload: a})
			}
		}
	case wsmsg.TopicExecPositions:
		out = append(out, staged{Topic: topic, Payload: m.positionsPayload()})
	case wsmsg.TopicExecOrders:
		out = append(out, staged{Topic: topic, Payload: m.ordersPayload()})
	case wsmsg.TopicExecFills:
		// make (not append-to-nil) so an empty fills list marshals to `[]`,
		// not `null` -- FillStore.ingest has no null guard, and m.fills is
		// nil until the first fill, so every zero-fill reconnect hits this.
		fills := make([]wsmsg.Fill, 0, len(m.fills))
		out = append(out, staged{Topic: topic, Payload: append(fills, m.fills...)})
	case wsmsg.TopicExecTrades:
		trades := make([]wsmsg.ClosedTradeRow, 0, len(m.trades))
		out = append(out, staged{Topic: topic, Payload: append(trades, m.trades...)})
	case wsmsg.TopicExecStatus:
		out = append(out, staged{Topic: topic, Payload: m.execStatus()})
	case wsmsg.TopicSysHealth:
		out = append(out, staged{Topic: topic, Payload: m.health})
	case wsmsg.TopicSysSession:
		out = append(out, staged{Topic: topic, Payload: m.session})
	case wsmsg.TopicSysBoot:
		out = append(out, staged{Topic: topic, Payload: m.boot})
	case wsmsg.TopicSysEvents:
		// make (not append-to-nil) so an empty events list marshals to `[]`,
		// not `null` -- already null-guarded on the UI side, but kept
		// consistent with the other four sites in this function.
		events := make([]wsmsg.SysEvent, 0, len(m.events))
		out = append(out, staged{Topic: topic, Payload: append(events, m.events...)})
	}
	// scanner.hit and config have no snapshot.
	return out
}

func (m *mirror) ordersPayload() []wsmsg.Order {
	out := make([]wsmsg.Order, 0, len(m.orders))
	for _, id := range sortedKeysOf(m.orders) {
		out = append(out, m.orders[id])
	}
	return out
}

// sortedKeysOf returns a map's keys sorted ascending, giving deterministic,
// test-stable iteration order for every keyed cache in the mirror regardless
// of the value type.
func sortedKeysOf[V any](mp map[string]V) []string {
	return slices.Sorted(maps.Keys(mp))
}
