// Package news polls Moomoo's Qot_GetSearchNews API (3263). Moomoo has no
// news push feed, so all requests share a quota-controlled polling lane.
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

// searchNews is detached from protobuf ownership before normalization.
type searchNews struct {
	Title, Source, URL, PublishTime string
	NewsSubType                     int32
	ViewCount                       int64
	RelatedSecurities               []string
}

type Poller struct {
	cfg       config.News
	r         requester
	pub       Publisher
	clk       clock.Clock
	symbols   func() SymbolPlan
	seen      map[string]seenArticle
	scheduler *scheduler
	limiter   limiter
}

func New(cfg config.News, r requester, pub Publisher, clk clock.Clock, symbols func() SymbolPlan) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, symbols: symbols, seen: map[string]seenArticle{}, scheduler: newScheduler()}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.WatchMs) * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			now, plan := p.clk.Now(), p.symbols()
			p.prune(now)
			symbol := p.scheduler.next(plan, now, time.Duration(p.cfg.ActiveRefreshMs)*time.Millisecond, time.Duration(p.cfg.ScannerRefreshMs)*time.Millisecond)
			if symbol != "" && p.limiter.allow(now) {
				p.scheduler.record(plan, symbol, now)
				p.pollSymbol(ctx, symbol, plan, now)
			}
		}
	}
}

func (p *Poller) pollSymbol(ctx context.Context, symbol string, plan SymbolPlan, now time.Time) {
	req := &newspb.C2S{Keyword: proto.String(symbol), MaxCount: proto.Int32(int32(p.cfg.MaxPerReq))}
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
		raw = append(raw, searchNews{Title: n.GetTitle(), Source: n.GetSource(), URL: n.GetUrl(), NewsSubType: n.GetNewsSubType(), PublishTime: n.GetPublishTime(), ViewCount: n.GetViewCount(), RelatedSecurities: append([]string(nil), n.GetRelatedSecurities()...)})
	}
	fresh := p.upsert(normalizeArticles(raw, symbol, plan, now, time.Duration(p.cfg.MaxAgeHours)*time.Hour), now)
	if len(fresh) > 0 {
		p.pub.Publish(wsmsg.TopicNews, "", fresh)
	}
}

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
