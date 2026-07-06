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
	Title  string
	Source string
	URL    string
}

type Poller struct {
	cfg     config.News
	r       requester
	pub     Publisher
	clk     clock.Clock
	symbols func() []string // focused + watchlist symbols to rotate through
	seen    map[string]bool // dedup keys
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
		raw = append(raw, searchNews{Title: n.GetTitle(), Source: n.GetSource(), URL: n.GetUrl()})
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
		})
	}
	return out
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
