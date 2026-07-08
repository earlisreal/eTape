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
}

// venueMeta is the static per-venue config the mirror needs to assemble exec.status.
type venueMeta struct {
	ID      string
	Broker  wsmsg.Broker
	AutoArm bool
	Note    string
	Gate    wsmsg.GateLimitsView
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
	rank map[string]wsmsg.ScannerRankPayload // key session
	news []wsmsg.NewsItem                    // bounded recent

	// execution
	accounts    map[string]wsmsg.AccountRow // key venue
	positions   map[string]exec.Position    // key "venue|symbol"; mapped w/ mark on read
	orders      map[string]wsmsg.Order      // key orderID
	fills       []wsmsg.Fill                // bounded recent
	venueStatus map[string]*wsmsg.VenueStatus
	masterArmed bool

	// system
	health wsmsg.HealthSnapshot
	events []wsmsg.SysEvent // bounded recent

	tapeCap, newsCap, fillsCap, eventsCap int
	venueOrder                            []string // stable venue order for exec.status
}

func newMirror(venues []venueMeta, global wsmsg.GlobalLimitsView, tapeCap, newsCap, fillsCap, eventsCap int) *mirror {
	m := &mirror{
		global:      global,
		quotes:      map[string]wsmsg.Quote{},
		books:       map[string]wsmsg.Book{},
		tape:        map[string][]wsmsg.Tick{},
		bars:        map[string][]wsmsg.Bar{},
		indicators:  map[string][]wsmsg.IndicatorPoint{},
		marks:       map[string]float64{},
		rank:        map[string]wsmsg.ScannerRankPayload{},
		accounts:    map[string]wsmsg.AccountRow{},
		positions:   map[string]exec.Position{},
		orders:      map[string]wsmsg.Order{},
		venueStatus: map[string]*wsmsg.VenueStatus{},
		tapeCap:     tapeCap, newsCap: newsCap, fillsCap: fillsCap, eventsCap: eventsCap,
	}
	for _, v := range venues {
		m.venueStatus[v.ID] = &wsmsg.VenueStatus{
			Venue: v.ID, Broker: v.Broker, Gate: v.Gate,
			VenueArmed: v.AutoArm, Note: v.Note,
		}
		m.venueOrder = append(m.venueOrder, v.ID)
		if v.AutoArm {
			m.masterArmed = true
		}
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
	case exec.PositionUpdate:
		m.positions[string(v.Position.Venue)+"|"+v.Position.Symbol] = v.Position
		return []staged{{Topic: wsmsg.TopicExecPositions, Payload: m.positionsPayload()}}
	case exec.AccountUpdate:
		a := mapAccount(v.Account)
		m.accounts[a.Venue] = a
		m.masterArmed = v.MasterArmed
		if vs := m.venueStatus[a.Venue]; vs != nil {
			vs.VenueArmed = v.VenueArmed
		}
		return []staged{
			{Topic: wsmsg.TopicExecAccount, Key: a.Venue, Payload: a},
			{Topic: wsmsg.TopicExecStatus, Payload: m.execStatus()},
		}
	case exec.StatusUpdate:
		m.masterArmed = v.MasterArmed
		if vs := m.venueStatus[string(v.Venue)]; vs != nil {
			vs.Connected = v.Connected
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
		for _, s := range sortedKeysOf(m.tape) {
			out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Tick(nil), m.tape[s]...)})
		}
	case wsmsg.TopicBars:
		for _, k := range sortedKeysOf(m.bars) {
			out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Bar(nil), m.bars[k]...)})
		}
	case wsmsg.TopicIndicator:
		for _, k := range sortedKeysOf(m.indicators) {
			out = append(out, staged{Topic: topic, Key: k, Payload: append([]wsmsg.IndicatorPoint(nil), m.indicators[k]...)})
		}
	case wsmsg.TopicScannerRank:
		for _, sess := range sortedKeysOf(m.rank) {
			out = append(out, staged{Topic: topic, Key: sess, Payload: m.rank[sess]})
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
		out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Fill(nil), m.fills...)})
	case wsmsg.TopicExecStatus:
		out = append(out, staged{Topic: topic, Payload: m.execStatus()})
	case wsmsg.TopicSysHealth:
		out = append(out, staged{Topic: topic, Payload: m.health})
	case wsmsg.TopicSysEvents:
		out = append(out, staged{Topic: topic, Payload: append([]wsmsg.SysEvent(nil), m.events...)})
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
