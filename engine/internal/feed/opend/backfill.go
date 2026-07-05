package opend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetbasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetticker"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// ErrHistoryQuotaExhausted is returned when a deep backfill would spend a
// history slot the account no longer has. Charts degrade to the quota-free
// cache (≤1,000 1m bars) with a logged warning — never silently.
var ErrHistoryQuotaExhausted = errors.New("opend: history K-line quota exhausted")

// maxAPIRows is moomoo's per-call cap for cache reads and history pages.
const maxAPIRows = 1000

// maxHistoryPages caps pagination (40k bars) as a runaway backstop.
const maxHistoryPages = 40

// backfill wraps the benchmarked cheap read paths: get_cur_kline ~9 ms,
// get_rt_ticker ~30 ms, get_order_book ~2.5 ms — all quota-free local-cache
// reads — plus the quota-spending Qot_RequestHistoryKL.
type backfill struct {
	rpc rpc
}

func newBackfill(r rpc) *backfill { return &backfill{rpc: r} }

func clampRows(n int) int32 {
	if n <= 0 || n > maxAPIRows {
		return maxAPIRows
	}
	return int32(n)
}

func retErr(protoID uint32, retType int32, msg string) error {
	return fmt.Errorf("opend: proto %d: retType=%d msg=%q", protoID, retType, msg)
}

// cachedBars1m reads the quota-free local-cache 1m K-line series (Qot_GetKL).
func (b *backfill) cachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	req := &qotgetkl.Request{C2S: &qotgetkl.C2S{
		RehabType: proto.Int32(int32(qotcommon.RehabType_RehabType_Forward)),
		KlType:    proto.Int32(int32(qotcommon.KLType_KLType_1Min)),
		Security:  sec,
		ReqNum:    proto.Int32(clampRows(n)),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetKL, req)
	if err != nil {
		return nil, err
	}
	var resp qotgetkl.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return nil, fmt.Errorf("get_kl decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return nil, retErr(ProtoQotGetKL, resp.GetRetType(), resp.GetRetMsg())
	}
	return decodeKLines(symbol, resp.GetS2C().GetKlList(), feed.Res1m)
}

func decodeKLines(symbol string, list []*qotcommon.KLine, res feed.Resolution) ([]feed.Bar, error) {
	bars := make([]feed.Bar, 0, len(list))
	for _, k := range list {
		bar, err := decodeKLine(symbol, k, res)
		if err != nil {
			return nil, err
		}
		bars = append(bars, bar)
	}
	return bars, nil
}

// recentTicks reads the quota-free local-cache tick series (Qot_GetTicker).
func (b *backfill) recentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	req := &qotgetticker.Request{C2S: &qotgetticker.C2S{
		Security:  sec,
		MaxRetNum: proto.Int32(clampRows(n)),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetTicker, req)
	if err != nil {
		return nil, err
	}
	var resp qotgetticker.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return nil, fmt.Errorf("get_ticker decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return nil, retErr(ProtoQotGetTicker, resp.GetRetType(), resp.GetRetMsg())
	}
	ticks := make([]feed.Tick, 0, len(resp.GetS2C().GetTickerList()))
	for _, t := range resp.GetS2C().GetTickerList() {
		ticks = append(ticks, decodeTicker(symbol, t))
	}
	return ticks, nil
}

// bookSnapshot reads the current 10-level order book (Qot_GetOrderBook).
func (b *backfill) bookSnapshot(ctx context.Context, symbol string) (feed.Book, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.Book{}, err
	}
	req := &qotgetorderbook.Request{C2S: &qotgetorderbook.C2S{
		Security: sec,
		Num:      proto.Int32(10), // API max for securities; entitlement-gated
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetOrderBook, req)
	if err != nil {
		return feed.Book{}, err
	}
	var resp qotgetorderbook.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return feed.Book{}, fmt.Errorf("get_order_book decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return feed.Book{}, retErr(ProtoQotGetOrderBook, resp.GetRetType(), resp.GetRetMsg())
	}
	s2c := resp.GetS2C()
	return feed.Book{
		Symbol: symbol,
		TsMs:   tsMs(max(s2c.GetSvrRecvTimeBidTimestamp(), s2c.GetSvrRecvTimeAskTimestamp())),
		Bids:   decodeBookLevels(s2c.GetOrderBookBidList()),
		Asks:   decodeBookLevels(s2c.GetOrderBookAskList()),
	}, nil
}

// quoteSnapshot reads the current basic quote (Qot_GetBasicQot).
func (b *backfill) quoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return feed.Quote{}, err
	}
	req := &qotgetbasicqot.Request{C2S: &qotgetbasicqot.C2S{
		SecurityList: []*qotcommon.Security{sec},
	}}
	f, err := b.rpc.Request(ctx, ProtoQotGetBasicQot, req)
	if err != nil {
		return feed.Quote{}, err
	}
	var resp qotgetbasicqot.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return feed.Quote{}, fmt.Errorf("get_basic_qot decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return feed.Quote{}, retErr(ProtoQotGetBasicQot, resp.GetRetType(), resp.GetRetMsg())
	}
	list := resp.GetS2C().GetBasicQotList()
	if len(list) == 0 {
		return feed.Quote{}, fmt.Errorf("opend: no basic quote returned for %s", symbol)
	}
	return decodeBasicQot(list[0])
}

// historyBars pulls deep history through the quota-tracked API, paging via
// NextReqKey. Res1m sets ExtendedTime (pre/post bars) and renders the range
// in ET with seconds; ResDay uses bare dates. The quota *guard* lives in
// Task 8 (OpenDFeed.HistoryBars), which needs the fetched-symbols memory;
// this method always issues the call.
func (b *backfill) historyBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) {
	sec, err := parseSymbol(symbol)
	if err != nil {
		return nil, err
	}
	klType, timeFmt := int32(qotcommon.KLType_KLType_Day), "2006-01-02"
	extended := false
	if res == feed.Res1m {
		klType, timeFmt = int32(qotcommon.KLType_KLType_1Min), "2006-01-02 15:04:05"
		extended = true
	}
	var (
		bars    []feed.Bar
		nextKey []byte
	)
	for page := 0; page < maxHistoryPages; page++ {
		c2s := &qotrequesthistorykl.C2S{
			RehabType:   proto.Int32(int32(qotcommon.RehabType_RehabType_Forward)),
			KlType:      proto.Int32(klType),
			Security:    sec,
			BeginTime:   proto.String(from.In(session.Loc()).Format(timeFmt)),
			EndTime:     proto.String(to.In(session.Loc()).Format(timeFmt)),
			MaxAckKLNum: proto.Int32(maxAPIRows),
		}
		if extended {
			c2s.ExtendedTime = proto.Bool(true)
		}
		if nextKey != nil {
			c2s.NextReqKey = nextKey
		}
		f, err := b.rpc.Request(ctx, ProtoQotRequestHistoryKL, &qotrequesthistorykl.Request{C2S: c2s})
		if err != nil {
			return nil, err
		}
		var resp qotrequesthistorykl.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			return nil, fmt.Errorf("request_history_kl decode: %w", err)
		}
		if resp.GetRetType() != 0 {
			return nil, retErr(ProtoQotRequestHistoryKL, resp.GetRetType(), resp.GetRetMsg())
		}
		pageBars, err := decodeKLines(symbol, resp.GetS2C().GetKlList(), res)
		if err != nil {
			return nil, err
		}
		bars = append(bars, pageBars...)
		nextKey = resp.GetS2C().GetNextReqKey()
		if len(nextKey) == 0 {
			return bars, nil
		}
	}
	slog.Warn("history pagination hit the page cap; result truncated",
		"symbol", symbol, "pages", maxHistoryPages, "bars", len(bars))
	return bars, nil
}

// historyQuota reads the account's history K-line quota (Qot_RequestHistoryKLQuota).
func (b *backfill) historyQuota(ctx context.Context) (used, remain int, err error) {
	req := &qotrequesthistoryklquota.Request{C2S: &qotrequesthistoryklquota.C2S{
		BGetDetail: proto.Bool(false),
	}}
	f, err := b.rpc.Request(ctx, ProtoQotRequestHistoryKLQuota, req)
	if err != nil {
		return 0, 0, err
	}
	var resp qotrequesthistoryklquota.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return 0, 0, fmt.Errorf("history_quota decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return 0, 0, retErr(ProtoQotRequestHistoryKLQuota, resp.GetRetType(), resp.GetRetMsg())
	}
	return int(resp.GetS2C().GetUsedQuota()), int(resp.GetS2C().GetRemainQuota()), nil
}
