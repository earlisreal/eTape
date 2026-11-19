package linearperp

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type trade struct {
	Ts       int64       `json:"T"`
	Price    json.Number `json:"p"`
	Volume   json.Number `json:"q"`
	MatchID  json.Number `json:"t"`
	BatchID  json.Number `json:"a"`
	MakerBuy bool        `json:"m"`
}

type barFrame struct {
	Open        json.Number `json:"o"`
	High        json.Number `json:"h"`
	Low         json.Number `json:"l"`
	Close       json.Number `json:"c"`
	Volume      json.Number `json:"v"`
	Turnover    json.Number `json:"q"`
	BinanceTime int64       `json:"t"`
	BingXTime   int64       `json:"T"`
}

func parseEvent(exchange string, t topic, raw json.RawMessage, frameMs int64) (feed.Event, int64) {
	switch t.kind {
	case tapeTopic:
		return parseTrades(exchange, t.symbol, raw), 0
	case bookTopic:
		var v struct {
			Event    string          `json:"e"`
			Time     int64           `json:"E"`
			First    int64           `json:"U"`
			Revision int64           `json:"u"`
			Bids     [][]json.Number `json:"b"`
			Asks     [][]json.Number `json:"a"`
			BXBids   [][]json.Number `json:"bids"`
			BXAsks   [][]json.Number `json:"asks"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return nil, 0
		}
		if exchange == BingX {
			v.Time, v.Revision, v.Bids, v.Asks = frameMs, frameMs, v.BXBids, v.BXAsks
		} else if v.Event != "depthUpdate" || v.First > v.Revision {
			return nil, 0
		}
		if v.Time <= 0 || v.Revision <= 0 {
			return nil, 0
		}
		bids, asks := levels(v.Bids, true), levels(v.Asks, false)
		if len(bids) == 0 || len(asks) == 0 || bids[0].Price >= asks[0].Price {
			return feed.BookEvent{Book: feed.Book{Symbol: t.symbol}}, v.Revision
		}
		return feed.BookEvent{Book: feed.Book{Symbol: t.symbol, TsMs: v.Time, Bids: bids, Asks: asks}}, v.Revision
	case barTopic:
		return parseBars(exchange, t.symbol, raw), 0
	case markTopic:
		var v struct {
			Event       string      `json:"e"`
			Ts          int64       `json:"E"`
			Mark        json.Number `json:"p"`
			Predicted   json.Number `json:"P"` // Distinguishes the other price from "p" during JSON decoding.
			Index       json.Number `json:"i"`
			Funding     json.Number `json:"r"`
			NextFunding int64       `json:"T"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Event != "markPriceUpdate" || v.Ts <= 0 || number(v.Mark) <= 0 {
			return nil, 0
		}
		return feed.QuoteEvent{Quote: feed.Quote{Symbol: t.symbol, TsMs: v.Ts, Mark: number(v.Mark),
			Index: number(v.Index), FundingRate: number(v.Funding), FundingKnown: v.Funding != "", NextFundingMs: v.NextFunding}}, v.Ts
	}
	return nil, 0
}

func parseTrades(exchange, symbol string, raw json.RawMessage) feed.Event {
	var rows []trade
	if exchange != BingX {
		var row trade
		if json.Unmarshal(raw, &row) != nil {
			return nil
		}
		row.MatchID = row.BatchID
		rows = []trade{row}
	} else if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	ticks := make([]feed.Tick, 0, len(rows))
	receipt := time.Now().UnixMilli()
	for _, row := range rows {
		id, err := row.MatchID.Int64()
		qty, valid := scaled(row.Volume)
		price := number(row.Price)
		if err != nil || id <= 0 || !valid || qty <= 0 || price <= 0 || row.Ts <= 0 {
			continue
		}
		side := feed.Buy
		if row.MakerBuy {
			side = feed.Sell
		}
		ticks = append(ticks, feed.Tick{Symbol: symbol, Seq: id, Price: price, Volume: qty, Dir: side,
			Turnover: instrument.DisplayQuantity(symbol, qty) * price, TsMs: row.Ts,
			RecvTsMs: receipt, Delivery: feed.DeliveryRealtime})
	}
	if len(ticks) == 0 {
		return nil
	}
	sort.SliceStable(ticks, func(i, j int) bool { return ticks[i].TsMs < ticks[j].TsMs })
	return feed.TicksEvent{Ticks: ticks}
}

func parseBars(exchange, symbol string, raw json.RawMessage) feed.Event {
	var rows []barFrame
	if exchange != BingX {
		var v struct {
			Bar barFrame `json:"k"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return nil
		}
		v.Bar.BingXTime = v.Bar.BinanceTime
		rows = []barFrame{v.Bar}
	} else if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	bars := make([]feed.Bar, 0, len(rows))
	for _, row := range rows {
		if b, valid := toBar(symbol, row.BingXTime, row.Open, row.High, row.Low, row.Close, row.Volume); valid {
			b.Turnover = number(row.Turnover)
			bars = append(bars, b)
		}
	}
	if len(bars) == 0 {
		return nil
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].BucketMs < bars[j].BucketMs })
	return feed.Bars1mEvent{Bars: bars}
}

func levels(raw [][]json.Number, bid bool) []feed.BookLevel {
	out := make([]feed.BookLevel, 0, len(raw))
	for _, row := range raw {
		if len(row) < 2 {
			continue
		}
		price := number(row[0])
		qty, valid := scaled(row[1])
		if valid && qty > 0 && price > 0 {
			out = append(out, feed.BookLevel{Price: price, Volume: qty})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if bid {
			return out[i].Price > out[j].Price
		}
		return out[i].Price < out[j].Price
	})
	return out
}
