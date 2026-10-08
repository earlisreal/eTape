package opend

import (
	"errors"
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetticker"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatekl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func captureProtocol(id uint32) bool {
	switch id {
	case ProtoQotGetTicker, ProtoQotUpdateTicker, ProtoQotGetOrderBook, ProtoQotUpdateOrderBook, ProtoQotGetKL, ProtoQotUpdateKL:
		return true
	}
	return false
}

func captureDescriptor(d feed.SourceMessage) bool {
	return d.Protocol != ProtoQotGetKL || !d.Matched || d.Timeframe == int32(qotcommon.KLType_KLType_1Min)
}

func requestSource(id uint32, request proto.Message) feed.SourceMessage {
	d := feed.SourceMessage{Protocol: id, Origin: "cache", Matched: true}
	switch r := request.(type) {
	case *qotgetkl.Request:
		d.Symbol = formatSymbol(r.GetC2S().GetSecurity())
		d.Timeframe = r.GetC2S().GetKlType()
		d.Adjustment = r.GetC2S().GetRehabType()
	case *qotgetticker.Request:
		d.Symbol = formatSymbol(r.GetC2S().GetSecurity())
	case *qotgetorderbook.Request:
		d.Symbol = formatSymbol(r.GetC2S().GetSecurity())
	default:
		d.Matched = false
	}
	return d
}

type captureResponse interface {
	proto.Message
	GetRetType() int32
	GetRetMsg() string
}
type tickerPayload interface {
	GetSecurity() *qotcommon.Security
	GetTickerList() []*qotcommon.Ticker
}
type minutePayload interface {
	GetSecurity() *qotcommon.Security
	GetKlList() []*qotcommon.KLine
}
type bookPayload interface {
	GetSecurity() *qotcommon.Security
	GetOrderBookBidList() []*qotcommon.OrderBook
	GetOrderBookAskList() []*qotcommon.OrderBook
}

type bookTop struct {
	Price    *float64
	Volume   *int64
	Orders   *int32
	HpVolume *float64
}
type recordedBBO struct {
	Bid, Ask                               *bookTop
	BidServerTime, AskServerTime           *string
	BidServerTimestamp, AskServerTimestamp *float64
	BookType                               *int32
}

func firstBookLevel(list []*qotcommon.OrderBook) *bookTop {
	if len(list) == 0 || list[0] == nil {
		return nil
	}
	b := list[0]
	return &bookTop{b.Price, b.Volume, b.OrederCount, b.HpVolume}
}

func optionalString(m protoreflect.Message, name protoreflect.Name) *string {
	f := m.Descriptor().Fields().ByName(name)
	if f == nil || !m.Has(f) {
		return nil
	}
	v := m.Get(f).String()
	return &v
}
func optionalFloat(m protoreflect.Message, name protoreflect.Name) *float64 {
	f := m.Descriptor().Fields().ByName(name)
	if f == nil || !m.Has(f) {
		return nil
	}
	v := m.Get(f).Float()
	return &v
}
func optionalInt(m protoreflect.Message, name protoreflect.Name) *int32 {
	f := m.Descriptor().Fields().ByName(name)
	if f == nil || !m.Has(f) {
		return nil
	}
	v := int32(m.Get(f).Int())
	return &v
}

// DecodeRecording runs only on the archive writer. The untouched source body
// and every indexed list item form one atomic group. BOOK retains no depth body.
func DecodeRecording(r feed.Recording) ([]feed.Recording, error) {
	d, ok := r.Data.(feed.SourceMessage)
	if !ok {
		return []feed.Recording{r}, errors.New("capture: missing request descriptor")
	}
	var response captureResponse
	book := false
	switch d.Protocol {
	case ProtoQotGetTicker:
		response = &qotgetticker.Response{}
	case ProtoQotUpdateTicker:
		response = &qotupdateticker.Response{}
	case ProtoQotGetKL:
		response = &qotgetkl.Response{}
	case ProtoQotUpdateKL:
		response = &qotupdatekl.Response{}
	case ProtoQotGetOrderBook:
		response = &qotgetorderbook.Response{}
		book = true
	case ProtoQotUpdateOrderBook:
		response = &qotupdateorderbook.Response{}
		book = true
	default:
		return nil, nil
	}
	body := r.Body
	if book {
		r.Body = nil
	}
	out := []feed.Recording{r}
	if d.Format != FmtProtobuf {
		return out, errors.New("capture: unsupported source format")
	}
	if err := proto.Unmarshal(body, response); err != nil {
		return out, fmt.Errorf("capture: decode: %w", err)
	}
	if response.GetRetType() != 0 {
		return out, fmt.Errorf("capture: provider rejection %d: %s", response.GetRetType(), response.GetRetMsg())
	}
	m := response.ProtoReflect()
	field := m.Descriptor().Fields().ByName("s2c")
	if field == nil || !m.Has(field) {
		return out, errors.New("capture: successful response lacks payload")
	}
	payload := m.Get(field).Message().Interface()
	switch p := payload.(type) {
	case tickerPayload:
		if p.GetSecurity() == nil {
			return out, errors.New("capture: ticker lacks security")
		}
		if len(p.GetTickerList()) > 4096 {
			return out, errors.New("capture: excessive ticker list")
		}
		symbol := formatSymbol(p.GetSecurity())
		for i, t := range p.GetTickerList() {
			delivery := decodeDeliverySource(t.GetPushDataType())
			if d.Protocol == ProtoQotGetTicker {
				delivery = feed.DeliveryCache
			}
			normalized := decodeTicker(symbol, t, delivery)
			ref := r.Source
			ref.Index = int32(i)
			normalized.Source = &ref
			out = append(out, feed.Recording{Kind: "print", Source: ref, Symbol: symbol, TimeMs: normalized.TsMs, Sequence: t.GetSequence(), Price: t.GetPrice(), Volume: t.GetVolume(), Direction: t.GetDir(), Condition: t.GetType(), Data: struct {
				Raw        *qotcommon.Ticker
				Normalized feed.Tick
			}{t, normalized}})
		}
	case bookPayload:
		if p.GetSecurity() == nil {
			return out, errors.New("capture: book lacks security")
		}
		pm := payload.ProtoReflect()
		bbo := recordedBBO{Bid: firstBookLevel(p.GetOrderBookBidList()), Ask: firstBookLevel(p.GetOrderBookAskList()), BidServerTime: optionalString(pm, "svrRecvTimeBid"), AskServerTime: optionalString(pm, "svrRecvTimeAsk"), BidServerTimestamp: optionalFloat(pm, "svrRecvTimeBidTimestamp"), AskServerTimestamp: optionalFloat(pm, "svrRecvTimeAskTimestamp"), BookType: optionalInt(pm, "orderBookType")}
		out = append(out, feed.Recording{Kind: "bbo", Source: r.Source, Symbol: formatSymbol(p.GetSecurity()), Data: bbo})
	case minutePayload:
		if d.Protocol == ProtoQotGetKL && (!d.Matched || d.Timeframe != int32(qotcommon.KLType_KLType_1Min)) {
			if d.Matched {
				return nil, nil
			}
			return out, errors.New("capture: unmatched K-line reply remains unclassified")
		}
		if p, ok := payload.(*qotupdatekl.S2C); ok && p.GetKlType() != int32(qotcommon.KLType_KLType_1Min) {
			return nil, nil
		}
		if p.GetSecurity() == nil {
			return out, errors.New("capture: minute lacks security")
		}
		if len(p.GetKlList()) > 4096 {
			return out, errors.New("capture: excessive minute list")
		}
		for i, k := range p.GetKlList() {
			bar, err := decodeKLine(formatSymbol(p.GetSecurity()), k, feed.Res1m)
			if err != nil {
				return out, err
			}
			ref := r.Source
			ref.Index = int32(i)
			bar.Source = ref
			out = append(out, feed.Recording{Kind: "minute", Source: ref, Symbol: bar.Symbol, Timeframe: "1m", TimeMs: bar.BucketMs, Data: struct {
				Raw        *qotcommon.KLine
				Normalized feed.Bar
				Request    feed.SourceMessage
			}{k, bar, d}})
		}
	}
	return out, nil
}

func (f Frame) sourceAt(index int) feed.SourceRef {
	if f.Source.Run == "" {
		return feed.SourceRef{}
	}
	ref := f.Source
	ref.Index = int32(index)
	return ref
}

func (f Frame) tickSourceAt(index int) *feed.SourceRef {
	if f.Source.Run == "" {
		return nil
	}
	ref := f.sourceAt(index)
	return &ref
}
