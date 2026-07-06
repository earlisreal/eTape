// Package scan is the pre-market/RTH rank scanner poller. It issues request/
// response protoIDs (3410 rank, 3215 filter, 3203 snapshot) through the OpenD
// client — no subscription quota — and publishes scanner.rank/scanner.hit.
package scan

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
	filterpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockfilter"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol    string
	ChangePct float64
	Last      float64
	Volume    int64
}

type Poller struct {
	cfg      config.Scan
	r        requester
	pub      Publisher
	clk      clock.Clock
	universe map[string]float64         // symbol -> actual float shares
	seen     map[string]map[string]bool // session -> symbol -> seen
	seenDay  int64                      // ET day of the current seen-sets
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk,
		universe: map[string]float64{}, seen: map[string]map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	p.refreshUniverse(ctx) // best-effort warm-up; logs+continues on error
	uniTick := p.clk.NewTicker(time.Duration(p.cfg.UniverseRefreshH) * time.Hour)
	defer uniTick.Stop()
	// Poll on a short base interval; the effective cadence is session-derived.
	base := p.clk.NewTicker(time.Duration(p.cfg.PremarketMs) * time.Millisecond)
	defer base.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-uniTick.C():
			p.refreshUniverse(ctx)
		case now := <-base.C():
			interval := p.pollInterval(now)
			if now.Sub(last) < interval {
				continue
			}
			last = now
			p.pollOnce(ctx, now)
		}
	}
}

func (p *Poller) pollInterval(now time.Time) time.Duration {
	if session.PhaseAt(now) == session.RTH {
		return time.Duration(p.cfg.RTHMs) * time.Millisecond
	}
	return time.Duration(p.cfg.PremarketMs) * time.Millisecond
}

func (p *Poller) sessionOf(now time.Time) string {
	switch session.PhaseAt(now) {
	case session.RTH:
		return "rth"
	case session.PostMarket:
		return "afterhours"
	default:
		return "premarket"
	}
}

func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	items, err := p.fetchRank(ctx)
	if err != nil {
		return // transient; next tick retries
	}
	p.resetSeenIfNewDay(now)
	rows := rankRows(items, p.universe, p.cfg)
	sess := p.sessionOf(now)
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}

// rankRows is the pure transform: apply float lookup + client-side thresholds.
func rankRows(items []rankItem, universe map[string]float64, cfg config.Scan) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if it.ChangePct < cfg.MinChangePct {
			continue
		}
		if cfg.MinVolume > 0 && it.Volume < cfg.MinVolume {
			continue
		}
		var floatPtr *float64
		if f, ok := universe[it.Symbol]; ok {
			if cfg.MaxFloatShares > 0 && f > cfg.MaxFloatShares {
				continue // known float exceeds cap -> reject
			}
			fv := f
			floatPtr = &fv
		}
		cp, lp := it.ChangePct, it.Last
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: it.Volume,
		})
	}
	return out
}

func (p *Poller) newHits(sess string, rows []wsmsg.ScannerRow) []string {
	s := p.seen[sess]
	if s == nil {
		s = map[string]bool{}
		p.seen[sess] = s
	}
	var hits []string
	for _, r := range rows {
		if !s[r.Symbol] {
			s[r.Symbol] = true
			hits = append(hits, r.Symbol)
		}
	}
	return hits
}

func (p *Poller) resetSeenIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]map[string]bool{}
	}
}

// fetchRank issues 3410 and normalizes the response to []rankItem.
func (p *Poller) fetchRank(ctx context.Context) ([]rankItem, error) {
	req := &rankpb.C2S{
		SortDir: proto.Int32(0), // descending = gainers
		Offset:  proto.Int32(0),
		Count:   proto.Int32(35),
	}
	// OpenD request messages wrap the inner C2S in a required outer Request{C2S:...}
	// (proto2 required field) — a bare C2S serializes to different bytes and OpenD
	// rejects it. Confirmed against every merged call site in feed/opend/backfill.go.
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank, &rankpb.Request{C2S: req})
	if err != nil {
		return nil, err
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 { // surface OpenD-side errors instead of looking like "0 rows"
		return nil, fmt.Errorf("rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{
			Symbol:    symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(),
			Last:      d.GetPreMarketPrice(),
			Volume:    d.GetPreMarketVolume(),
		})
	}
	return out, nil
}

// refreshUniverse loads the low-float universe via 3215 (FLOAT_SHARE is in
// THOUSANDS on the wire; convert to actual shares here, once).
func (p *Poller) refreshUniverse(ctx context.Context) {
	req := &filterpb.C2S{
		Begin:  proto.Int32(0),
		Num:    proto.Int32(200),
		Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), // required field (US-only scope)
		BaseFilterList: []*filterpb.BaseFilter{{
			FieldName: proto.Int32(int32(filterpb.StockField_StockField_FloatShare)),
			FilterMin: proto.Float64(0),
			FilterMax: proto.Float64(p.cfg.MaxFloatShares / 1000.0), // actual -> thousands for the request
		}},
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotStockFilter, &filterpb.Request{C2S: req})
	if err != nil {
		return
	}
	var resp filterpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil || resp.GetRetType() != 0 {
		return
	}
	uni := map[string]float64{}
	for _, d := range resp.GetS2C().GetDataList() {
		sym := symbolOf(d.GetSecurity())
		for _, bd := range d.GetBaseDataList() {
			if bd.GetFieldName() == int32(filterpb.StockField_StockField_FloatShare) {
				uni[sym] = bd.GetValue() * 1000.0 // thousands -> actual
			}
		}
	}
	if len(uni) > 0 {
		p.universe = uni
	}
}

// symbolOf renders a moomoo Security as eTape's "US.<code>" convention.
func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode() // US-only scope (CLAUDE.md); Market is always QotMarket_US here
}
