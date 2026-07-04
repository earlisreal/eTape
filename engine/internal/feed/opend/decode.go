package opend

import (
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatebasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatekl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

// marketByPrefix maps eTape's canonical symbol prefixes to QotMarket values.
// US is the product scope; HK/CC/SH/SZ exist for dev smoke tests only
// (weekends: CC trades 24/7, HK LV1 covers quotes+ticker+1-level book).
var marketByPrefix = map[string]int32{
	"US": int32(qotcommon.QotMarket_QotMarket_US_Security),   // 11
	"HK": int32(qotcommon.QotMarket_QotMarket_HK_Security),   // 1
	"SH": int32(qotcommon.QotMarket_QotMarket_CNSH_Security), // 21
	"SZ": int32(qotcommon.QotMarket_QotMarket_CNSZ_Security), // 22
	"CC": int32(qotcommon.QotMarket_QotMarket_CC_Security),   // 91
}

var prefixByMarket = func() map[int32]string {
	m := make(map[int32]string, len(marketByPrefix))
	for p, id := range marketByPrefix {
		m[id] = p
	}
	return m
}()

// parseSymbol converts a domain symbol ("US.AAPL"; bare defaults to US) to a
// pb Security.
func parseSymbol(sym string) (*qotcommon.Security, error) {
	prefix, code, found := strings.Cut(sym, ".")
	if !found {
		prefix, code = "US", sym
	}
	market, ok := marketByPrefix[prefix]
	if !ok {
		return nil, fmt.Errorf("opend: unknown market prefix %q in symbol %q", prefix, sym)
	}
	if code == "" {
		return nil, fmt.Errorf("opend: empty code in symbol %q", sym)
	}
	return &qotcommon.Security{Market: proto.Int32(market), Code: proto.String(code)}, nil
}

// formatSymbol converts a pb Security back to the canonical prefixed form.
func formatSymbol(s *qotcommon.Security) string {
	if p, ok := prefixByMarket[s.GetMarket()]; ok {
		return p + "." + s.GetCode()
	}
	return fmt.Sprintf("M%d.%s", s.GetMarket(), s.GetCode())
}

// tsMs converts moomoo's epoch-seconds float64 timestamps to epoch ms.
func tsMs(sec float64) int64 { return int64(math.Round(sec * 1000)) }

func decodeDirection(d int32) feed.Direction {
	switch qotcommon.TickerDirection(d) {
	case qotcommon.TickerDirection_TickerDirection_Bid:
		return feed.Buy
	case qotcommon.TickerDirection_TickerDirection_Ask:
		return feed.Sell
	}
	return feed.Neutral
}

func decodeTicker(symbol string, t *qotcommon.Ticker) feed.Tick {
	return feed.Tick{
		Symbol:   symbol,
		Seq:      t.GetSequence(),
		TsMs:     tsMs(t.GetTimestamp()),
		Price:    t.GetPrice(),
		Volume:   t.GetVolume(),
		Turnover: t.GetTurnover(),
		Dir:      decodeDirection(t.GetDir()),
		RecvTsMs: tsMs(t.GetRecvTime()),
	}
}

func decodeBasicQot(b *qotcommon.BasicQot) (feed.Quote, error) {
	if b.GetSecurity() == nil {
		return feed.Quote{}, fmt.Errorf("opend: BasicQot without security")
	}
	return feed.Quote{
		Symbol:    formatSymbol(b.GetSecurity()),
		TsMs:      tsMs(b.GetUpdateTimestamp()),
		Last:      b.GetCurPrice(),
		Open:      b.GetOpenPrice(),
		High:      b.GetHighPrice(),
		Low:       b.GetLowPrice(),
		PrevClose: b.GetLastClosePrice(),
		Volume:    b.GetVolume(),
		Turnover:  b.GetTurnover(),
	}, nil
}

func decodeBookLevels(list []*qotcommon.OrderBook) []feed.BookLevel {
	out := make([]feed.BookLevel, 0, len(list))
	for _, l := range list {
		out = append(out, feed.BookLevel{
			Price:  l.GetPrice(),
			Volume: l.GetVolume(),
			// Generated-code typo in the SDK proto: "OrederCount". Kept as-is —
			// the pb is committed, regeneration would reproduce it anyway.
			Orders: l.GetOrederCount(),
		})
	}
	return out
}

// decodeKLine is the ONLY place moomoo K-line time labeling is normalized.
// Verified live 2026-07-05: intraday K-lines label the bucket END (US.AAPL
// RTH day = 390 bars stamped 09:31:00..16:00:00, closing auction volume on
// the 16:00:00 bar), so Res1m subtracts one 60 s span to get eTape's
// bucket-START key. Daily bars are labeled with their own date — no shift.
// A zero Timestamp is an error: guessing from the Time string would silently
// mis-bucket HK symbols (their Time strings are HK-local).
func decodeKLine(symbol string, k *qotcommon.KLine, res feed.Resolution) (feed.Bar, error) {
	if k.GetTimestamp() == 0 {
		return feed.Bar{}, fmt.Errorf("opend: K-line for %s has no Timestamp (time=%q)", symbol, k.GetTime())
	}
	bucket := tsMs(k.GetTimestamp())
	if res == feed.Res1m {
		bucket -= 60_000
	}
	return feed.Bar{
		Symbol:   symbol,
		BucketMs: bucket,
		O:        k.GetOpenPrice(),
		H:        k.GetHighPrice(),
		L:        k.GetLowPrice(),
		C:        k.GetClosePrice(),
		Volume:   k.GetVolume(),
		Turnover: k.GetTurnover(),
	}, nil
}

// DecodePush converts a push frame into domain events. Unknown protoIDs and
// deliberately-unused pushes (RT time-share) return (nil, nil); malformed
// bodies or non-zero RetType return an error so the caller can count them.
func DecodePush(f Frame) ([]feed.Event, error) {
	switch f.ProtoID {
	case ProtoQotUpdateTicker:
		var resp qotupdateticker.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: ticker push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: ticker push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		symbol := formatSymbol(s2c.GetSecurity())
		ticks := make([]feed.Tick, 0, len(s2c.GetTickerList()))
		for _, t := range s2c.GetTickerList() {
			ticks = append(ticks, decodeTicker(symbol, t))
		}
		if len(ticks) == 0 {
			return nil, nil
		}
		return []feed.Event{feed.TicksEvent{Ticks: ticks}}, nil

	case ProtoQotUpdateBasicQot:
		var resp qotupdatebasicqot.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: basicqot push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: basicqot push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		var evs []feed.Event
		for _, b := range resp.GetS2C().GetBasicQotList() {
			q, err := decodeBasicQot(b)
			if err != nil {
				return nil, err
			}
			evs = append(evs, feed.QuoteEvent{Quote: q})
		}
		return evs, nil

	case ProtoQotUpdateOrderBook:
		var resp qotupdateorderbook.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: book push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: book push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		book := feed.Book{
			Symbol: formatSymbol(s2c.GetSecurity()),
			TsMs:   tsMs(math.Max(s2c.GetSvrRecvTimeBidTimestamp(), s2c.GetSvrRecvTimeAskTimestamp())),
			Bids:   decodeBookLevels(s2c.GetOrderBookBidList()),
			Asks:   decodeBookLevels(s2c.GetOrderBookAskList()),
		}
		return []feed.Event{feed.BookEvent{Book: book}}, nil

	case ProtoQotUpdateKL:
		var resp qotupdatekl.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("opend: kl push decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, fmt.Errorf("opend: kl push retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
		}
		s2c := resp.GetS2C()
		// eTape only subscribes K_1M; ignore anything else defensively.
		if s2c.GetKlType() != int32(qotcommon.KLType_KLType_1Min) {
			return nil, nil
		}
		symbol := formatSymbol(s2c.GetSecurity())
		bars := make([]feed.Bar, 0, len(s2c.GetKlList()))
		for _, k := range s2c.GetKlList() {
			b, err := decodeKLine(symbol, k, feed.Res1m)
			if err != nil {
				return nil, err
			}
			bars = append(bars, b)
		}
		if len(bars) == 0 {
			return nil, nil
		}
		return []feed.Event{feed.Bars1mEvent{Bars: bars}}, nil
	}
	return nil, nil
}
