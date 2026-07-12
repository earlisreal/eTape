// Package news is the poll-only news aggregator (Qot_GetSearchNews, 3263). No
// push exists for news; ordering is by engine-stamped seen_at, dedup by URL.
package news

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	newspb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsearchnews"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// searchNews is the poller-internal normalized item (decouples the transform
// from the pb type for testing).
type searchNews struct {
	Title       string
	Source      string
	URL         string
	NewsSubType int32
	PublishTime string
	ViewCount   int64
}

type Poller struct {
	cfg     config.News
	r       requester
	pub     Publisher
	clk     clock.Clock
	symbols func() []string // focused + watchlist symbols to rotate through
	seen    map[string]bool // dedup keys
	seenDay int64           // ET day of the current seen-set (0 forces a reset on first tick)
}

func New(cfg config.News, r requester, pub Publisher, clk clock.Clock, symbols func() []string) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, symbols: symbols, seen: map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.WatchMs) * time.Millisecond)
	defer tick.Stop()
	idx := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.resetIfNewDay(p.clk.Now())
			syms := p.symbols()
			if len(syms) == 0 {
				continue
			}
			sym := syms[idx%len(syms)]
			idx++
			p.pollSymbol(ctx, sym)
		}
	}
}

func (p *Poller) pollSymbol(ctx context.Context, symbol string) {
	req := &newspb.C2S{
		Keyword:  proto.String(symbol),
		MaxCount: proto.Int32(int32(p.cfg.MaxPerReq)),
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSearchNews, &newspb.Request{C2S: req})
	if err != nil {
		return
	}
	var resp newspb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil || resp.GetRetType() != 0 {
		return
	}
	raw := make([]searchNews, 0)
	for _, n := range resp.GetS2C().GetSearchNewsList() {
		raw = append(raw, searchNews{
			Title: n.GetTitle(), Source: n.GetSource(), URL: n.GetUrl(),
			NewsSubType: n.GetNewsSubType(), PublishTime: n.GetPublishTime(), ViewCount: n.GetViewCount(),
		})
	}
	seenAt := p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	fresh := p.dedup(normalize(raw, symbol, seenAt))
	if len(fresh) > 0 {
		p.pub.Publish(wsmsg.TopicNews, "", fresh)
	}
}

func normalize(raw []searchNews, symbol, seenAt string) []wsmsg.NewsItem {
	out := make([]wsmsg.NewsItem, 0, len(raw))
	for _, n := range raw {
		out = append(out, wsmsg.NewsItem{
			Symbol: symbol, Headline: n.Title, Source: n.Source, URL: n.URL, SeenAt: seenAt,
			PublishedAt: parsePublishTime(n.PublishTime), ViewCount: n.ViewCount, Type: mapNewsType(n.NewsSubType),
		})
	}
	return out
}

// mapNewsType maps moomoo's NewsSubType enum (0=ALL, 1=NEWS, 2=NOTICE,
// 3=RATING) to the wire "type" values. Both the ALL/0 catch-all and any
// unrecognized/future subtype fall back to "news" — the most common
// category — rather than an empty string, so the UI never has to render a
// blank type badge.
func mapNewsType(subType int32) string {
	switch subType {
	case 2:
		return "notice"
	case 3:
		return "rating"
	default:
		return "news"
	}
}

// parsePublishTime parses moomoo's bare "yyyy-MM-dd HH:mm:ss" PublishTime
// string (no timezone; always ET) and returns it as a UTC ISO-8601 string
// in the same format pollSymbol uses for SeenAt. On parse failure (empty or
// malformed input) it returns "" — callers fall back to SeenAt.
func parsePublishTime(pt string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", pt, session.Loc())
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// resetIfNewDay clears the dedup seen-set on the ET-day boundary, so the map
// doesn't grow unboundedly for the life of the process. Mirrors the scan
// poller's resetIfNewDay (internal/scan/scan.go).
func (p *Poller) resetIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]bool{}
	}
}

func (p *Poller) dedup(items []wsmsg.NewsItem) []wsmsg.NewsItem {
	out := make([]wsmsg.NewsItem, 0, len(items))
	for _, it := range items {
		key := it.URL
		if key == "" {
			key = it.Symbol + "|" + it.Headline
		}
		if p.seen[key] {
			continue
		}
		p.seen[key] = true
		out = append(out, it)
	}
	return out
}
