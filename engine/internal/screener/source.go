package screener

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	screenpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockscreen"
	"github.com/earlisreal/eTape/engine/internal/hist/alpaca"
	"github.com/earlisreal/eTape/engine/internal/hist/yahoo"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type Requester interface {
	Request(context.Context, uint32, proto.Message) (opend.Frame, error)
}

type Discovery struct {
	Stocks     []Instrument
	Universe   int
	Warning    string
	BatchDaily func(context.Context, []Instrument) (map[string][]feed.Bar, error)
}

type Source interface {
	Discover(context.Context, wsmsg.ScreenerFilters, time.Time, time.Time) (Discovery, error)
	Daily(context.Context, Instrument, time.Time) ([]feed.Bar, error)
}

type MarketSource struct {
	r      Requester
	yahoo  *yahoo.Client
	alpaca *alpaca.Client
}

func NewMarketSource(r Requester, yahoo *yahoo.Client, alpaca *alpaca.Client) *MarketSource {
	return &MarketSource{r: r, yahoo: yahoo, alpaca: alpaca}
}

func (s *MarketSource) Discover(ctx context.Context, f wsmsg.ScreenerFilters, date, now time.Time) (Discovery, error) {
	discovery, err := s.discoverOpenD(ctx, f, date, now)
	if ctx.Err() != nil {
		return Discovery{}, ctx.Err()
	}
	if err == nil {
		return discovery, nil
	}
	discovery, fallbackErr := s.discoverYahoo(ctx, f, date)
	if fallbackErr != nil {
		return Discovery{}, fmt.Errorf("OpenD stock discovery unavailable (%v). Yahoo Finance fallback failed: %w", err, fallbackErr)
	}
	return discovery, nil
}

func (s *MarketSource) discoverOpenD(ctx context.Context, f wsmsg.ScreenerFilters, date, now time.Time) (Discovery, error) {
	ctx = opend.WithBackgroundRequest(ctx)
	frame, err := s.r.Request(ctx, opend.ProtoQotGetStaticInfo, &staticpb.Request{C2S: &staticpb.C2S{Market: proto.Int32(11), SecType: proto.Int32(3)}})
	if err != nil {
		return Discovery{}, err
	}
	var response staticpb.Response
	if err = proto.Unmarshal(frame.Body, &response); err != nil {
		return Discovery{}, err
	}
	if response.GetRetType() != 0 {
		return Discovery{}, fmt.Errorf("stock universe: %s", response.GetRetMsg())
	}
	var stocks []Instrument
	seen := map[string]bool{}
	for _, entry := range response.GetS2C().GetStaticInfoList() {
		b := entry.GetBasic()
		if b.GetSecType() != 3 || b.GetDelisting() || b.GetSecurity().GetMarket() != 11 {
			continue
		}
		// OpenD's listed US exchanges; exclude OTC/Pink and non-equity types.
		exchange := b.GetExchType()
		if exchange != 4 && exchange != 5 && exchange != 7 {
			continue
		}
		code := b.GetSecurity().GetCode()
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		listing, _ := time.ParseInLocation("2006-01-02", strings.Split(b.GetListTime(), " ")[0], session.Loc())
		stocks = append(stocks, Instrument{Symbol: "US." + code, Name: b.GetName(), ListingDate: listing})
	}
	slices.SortFunc(stocks, func(a, b Instrument) int { return strings.Compare(a.Symbol, b.Symbol) })
	universe := len(stocks)
	if universe == 0 {
		return Discovery{}, fmt.Errorf("OpenD returned no listed US stocks")
	}
	warning := ""
	// The provider's rolling-high prefilter is a superset of the selected
	// lookback. Never use today's screen when evaluating a prior session.
	if date.Equal(session.Schedule(now).Date) {
		codes, screenErr := s.highCandidates(ctx, f)
		if errors.Is(screenErr, opend.ErrNotConnected) {
			return Discovery{}, screenErr
		}
		if screenErr != nil {
			warning = "High prefilter unavailable; checking the full US stock universe."
		} else {
			stocks = slices.DeleteFunc(stocks, func(stock Instrument) bool { return !codes[stock.Symbol] })
		}
	}
	if err = s.shares(ctx, stocks); err != nil {
		if errors.Is(err, opend.ErrNotConnected) || ctx.Err() != nil {
			return Discovery{}, err
		}
		warning = strings.TrimSpace(warning + " Some market-cap data is unavailable; active market-cap bounds exclude stocks missing it.")
	}
	return Discovery{Stocks: stocks, Universe: universe, Warning: warning}, nil
}

func (s *MarketSource) highCandidates(ctx context.Context, f wsmsg.ScreenerFilters) (map[string]bool, error) {
	days := 60
	if f.Lookback == "trading-days" {
		days = min(days, f.TradingDays)
	}
	near := f.NearPct
	if f.Mode == "new" {
		near = 0
	}
	// A deliberately broad percentage bound also tolerates providers that
	// represent the ratio as a fraction. Exact qualification uses daily bars.
	query := &screenpb.C2S{
		FilterList: []*screenpb.ScreenQuery{
			{SimpleFieldQuery: &screenpb.QuerySimpleField{SimpleField: proto.Int32(1), ScreenValueList: []int64{2}}},
			{CumulativePropertyQuery: &screenpb.QueryPropertyCumulative{Property: &screenpb.PropertyCumulative{Name: proto.Int32(3107), Days: proto.Uint32(uint32(days))}, FilterMin: &screenpb.Boundary{Value: proto.Float64(-near), Includes: proto.Bool(true)}}},
		},
		RetrieveList: []*screenpb.RetrieveQuery{{BasicProperty: &screenpb.PropertyBasic{Name: proto.Int32(1101)}}},
		Sort:         &screenpb.Sort{Direction: proto.Int32(1), BasicProperty: &screenpb.PropertyBasic{Name: proto.Int32(1101)}},
		PageCount:    proto.Int32(200),
	}
	codes := map[string]bool{}
	for page := 0; page < 100; page++ {
		query.PageFrom = proto.Int32(int32(page * 200))
		frame, err := s.r.Request(ctx, opend.ProtoQotGetStockScreen, &screenpb.Request{C2S: query})
		if err != nil {
			return nil, err
		}
		var response screenpb.Response
		if err = proto.Unmarshal(frame.Body, &response); err != nil {
			return nil, err
		}
		if response.GetRetType() != 0 {
			return nil, fmt.Errorf("high discovery: %s", response.GetRetMsg())
		}
		data := response.GetS2C()
		before := len(codes)
		for _, item := range data.GetDataList() {
			for _, result := range item.GetResults() {
				if b := result.GetBasicPropertyResult(); b != nil && b.GetProperty().GetName() == 1101 && b.GetSval() != "" {
					codes["US."+strings.TrimPrefix(b.GetSval(), "US.")] = true
				}
			}
		}
		if (len(data.GetDataList()) > 0 && len(codes) == before) || (len(data.GetDataList()) == 0 && int(data.GetAllCount()) > page*200) {
			return nil, fmt.Errorf("high discovery returned incomplete codes")
		}
		if data.GetLastPage() != 0 || (data.AllCount != nil && (page+1)*200 >= int(data.GetAllCount())) {
			return codes, nil
		}
		if len(codes) == before {
			return nil, fmt.Errorf("high discovery pagination made no progress")
		}
	}
	return nil, fmt.Errorf("high discovery exceeded pagination limit")
}

func (s *MarketSource) shares(ctx context.Context, stocks []Instrument) error {
	var failure error
	for offset := 0; offset < len(stocks); offset += 300 {
		batch := stocks[offset:min(offset+300, len(stocks))]
		securities := make([]*qotcommon.Security, 0, len(batch))
		for _, stock := range batch {
			securities = append(securities, &qotcommon.Security{Market: proto.Int32(11), Code: proto.String(strings.TrimPrefix(stock.Symbol, "US."))})
		}
		frame, err := s.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot, &snappb.Request{C2S: &snappb.C2S{SecurityList: securities}})
		if err != nil {
			if errors.Is(err, opend.ErrNotConnected) || ctx.Err() != nil {
				return err
			}
			failure = err
			continue
		}
		var response snappb.Response
		if err = proto.Unmarshal(frame.Body, &response); err != nil {
			failure = err
			continue
		}
		if response.GetRetType() != 0 {
			failure = fmt.Errorf("market-cap snapshot unavailable")
			continue
		}
		byCode := map[string]float64{}
		for _, snapshot := range response.GetS2C().GetSnapshotList() {
			if equity := snapshot.GetEquityExData(); equity != nil {
				byCode["US."+snapshot.GetBasic().GetSecurity().GetCode()] = float64(equity.GetIssuedShares())
			}
		}
		for i := range batch {
			batch[i].Shares = byCode[batch[i].Symbol]
		}
	}
	return failure
}

var yahooTicker = regexp.MustCompile(`^[A-Z0-9]+([.-][A-Z0-9]+)?$`)

// Alpaca's SIP history begins in 2016. An ATH needs the listing boundary.
var alpacaHistoryFloor = time.Date(2016, 1, 1, 0, 0, 0, 0, session.Loc())

func (s *MarketSource) discoverYahoo(ctx context.Context, f wsmsg.ScreenerFilters, date time.Time) (Discovery, error) {
	quotes, err := s.yahoo.ListedUSEquities(ctx)
	if err != nil {
		return Discovery{}, err
	}
	result := Discovery{Warning: "OpenD unavailable. Using Yahoo Finance for US stock discovery."}
	closeAt := session.Schedule(date).Close
	nextDay := date.AddDate(0, 0, 1)
	near := f.NearPct
	if f.Mode == "new" {
		near = 0
	}
	seen := make(map[string]bool, len(quotes))
	for _, q := range quotes {
		if q.Type != "EQUITY" || !yahooTicker.MatchString(q.Symbol) {
			continue
		}
		switch q.Exchange {
		case "ASE", "NAS", "NCM", "NGM", "NMS", "NYQ":
		default:
			continue
		}
		stock := Instrument{Symbol: "US." + strings.ReplaceAll(q.Symbol, "-", "."), Name: q.Name}
		if seen[stock.Symbol] {
			continue
		}
		seen[stock.Symbol] = true
		result.Universe++
		if q.FirstTradeMs != 0 {
			stock.ListingDate = session.Schedule(time.UnixMilli(q.FirstTradeMs)).Date
		}
		if positive(q.MarketCap) && positive(q.Price) {
			stock.Shares = q.MarketCap / q.Price
		}
		quoteTime := time.Unix(q.MarketTime, 0)
		// The 52-week high is a lower bound on ATH. Use a broad cutoff against
		// the session high (an upper bound on the close), then check daily bars.
		if f.Lookback == "all-time" && (s.alpaca == nil || stock.ListingDate.Before(alpacaHistoryFloor)) &&
			!quoteTime.Before(closeAt) && quoteTime.Before(nextDay) && positive(q.DayHigh) && positive(q.FiftyTwoWeekHigh) &&
			(q.FiftyTwoWeekHigh-q.DayHigh)/q.FiftyTwoWeekHigh*100 > near+1 {
			continue
		}
		result.Stocks = append(result.Stocks, stock)
	}
	if result.Universe == 0 {
		return Discovery{}, errors.New("stock discovery: Yahoo returned no listed US stocks")
	}
	if s.alpaca != nil {
		result.Warning += " Uses delayed Alpaca SIP history where it covers the lookback, with Yahoo daily history as a fallback."
		result.BatchDaily = s.alpacaHistory(f, date)
	}
	return result, nil
}

func (s *MarketSource) alpacaHistory(f wsmsg.ScreenerFilters, date time.Time) func(context.Context, []Instrument) (map[string][]feed.Bar, error) {
	start := lookbackStart(f, date)
	windowStart := start
	if f.Lookback != "all-time" {
		// Rows always display 21-session change. Longer SMA bounds need more history.
		sessions := 22
		if f.MaxExtensionPct > 0 {
			sessions = max(sessions, f.ExtensionSMA)
		}
		if f.AboveSMA50 {
			sessions = max(sessions, 50)
		}
		if f.AboveSMA200 {
			sessions = 200
		}
		recent := date
		for i := 1; i < sessions; i++ {
			recent = session.PreviousTradingDay(recent)
		}
		if recent.Before(windowStart) {
			windowStart = recent
		}
	}
	return func(ctx context.Context, stocks []Instrument) (map[string][]feed.Bar, error) {
		from := date
		var symbols []string
		for _, stock := range stocks {
			coverageStart := start
			if stock.ListingDate.After(coverageStart) {
				coverageStart = stock.ListingDate
			}
			if coverageStart.Before(alpacaHistoryFloor) || !coverageStart.Before(date) {
				continue
			}
			stockFrom := windowStart
			if stock.ListingDate.After(stockFrom) {
				stockFrom = stock.ListingDate
			}
			if stockFrom.Before(from) {
				from = stockFrom
			}
			symbols = append(symbols, stock.Symbol)
		}
		if len(symbols) == 0 {
			return nil, nil
		}
		if from.Before(alpacaHistoryFloor) {
			from = alpacaHistoryFloor
		}
		histories, err := s.alpaca.ScreenerDailyBatch(ctx, symbols, from, date)
		if err != nil {
			return nil, err
		}
		for _, stock := range stocks {
			if bars := histories[stock.Symbol]; len(bars) > 0 && stock.ListingDate.After(from) {
				histories[stock.Symbol] = slices.DeleteFunc(bars, func(bar feed.Bar) bool {
					return bar.BucketMs < stock.ListingDate.UnixMilli()
				})
			}
		}
		return histories, nil
	}
}

func (s *MarketSource) Daily(ctx context.Context, stock Instrument, date time.Time) ([]feed.Bar, error) {
	from := stock.ListingDate
	if from.IsZero() {
		from = date.AddDate(-12, 0, 0)
	}
	return s.yahoo.ScreenerDailyBars(ctx, stock.Symbol, from, date.AddDate(0, 0, 1))
}

// DemoSource reads only the throwaway synthetic archive, never a live provider.
type DemoSource struct {
	Symbols []string
	Read    func(string) ([]feed.Bar, error)
}

func (s DemoSource) Discover(ctx context.Context, _ wsmsg.ScreenerFilters, _ time.Time, _ time.Time) (Discovery, error) {
	var stocks []Instrument
	for _, symbol := range s.Symbols {
		if err := ctx.Err(); err != nil {
			return Discovery{}, err
		}
		bars, err := s.Read(symbol)
		if err != nil || len(bars) == 0 {
			continue
		}
		stocks = append(stocks, Instrument{Symbol: symbol, Name: "Demo " + strings.TrimPrefix(symbol, "US."), ListingDate: session.Schedule(time.UnixMilli(bars[0].BucketMs)).Date, Shares: 100_000_000})
	}
	return Discovery{Stocks: stocks, Universe: len(stocks), Warning: "Demo: generated daily history; no live market data."}, nil
}
func (s DemoSource) Daily(ctx context.Context, stock Instrument, _ time.Time) ([]feed.Bar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bars, err := s.Read(stock.Symbol)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(bars), func(b feed.Bar) bool { return !session.IsTradingDay(time.UnixMilli(b.BucketMs)) }), nil
}
