package watchlist

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// watchlistKey is the single publish key for the one global list.
const watchlistKey = ""

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// Poller ticks on interval, polls one batched 3203 for the whole list, and
// publishes a full watchlist.rows snapshot. The Run goroutine is the ONLY
// publisher of the topic — Poke wakes it for an immediate membership push +
// fresh poll, so no mutex guards the row cache (Run-goroutine-only).
type Poller struct {
	list     *List
	r        requester
	pub      Publisher
	clk      clock.Clock
	interval time.Duration
	poke     chan struct{}
	rows     map[string]wsmsg.WatchlistRow // last-known row per symbol (cache)
	lastRef  *string                       // RFC3339 of last successful poll; nil until first
}

func New(list *List, r requester, pub Publisher, clk clock.Clock, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return &Poller{
		list: list, r: r, pub: pub, clk: clk, interval: interval,
		poke: make(chan struct{}, 1),
		rows: map[string]wsmsg.WatchlistRow{},
	}
}

func (p *Poller) Run(ctx context.Context) error {
	// Publish membership immediately so the mirror + late subscribers (and the
	// demo-entry barrier) see the seeded symbols without waiting a full tick.
	p.publishMembership()
	tick := p.clk.NewTicker(p.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.pollAndPublish(ctx)
		case <-p.poke:
			p.publishMembership() // instant: new symbols appear as dashes, removed vanish
			p.pollAndPublish(ctx) // then fresh data
		}
	}
}

// Poke is a non-blocking wake (coalesces if a poke is already queued).
func (p *Poller) Poke() {
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

// publishMembership publishes the current membership with whatever rows are
// cached (unknown symbols render as dashes UI-side).
func (p *Poller) publishMembership() {
	syms := p.list.Symbols()
	p.pub.Publish(wsmsg.TopicWatchlistRows, watchlistKey, p.buildPayload(syms))
}

// pollAndPublish issues one batched 3203 (binary-split on batch failure),
// updates the row cache, stamps RefreshedAt, and publishes. Empty list → zero
// requests but still publishes an empty snapshot.
func (p *Poller) pollAndPublish(ctx context.Context) {
	syms := p.list.Symbols()
	if len(syms) > 0 {
		got := map[string]*snappb.Snapshot{}
		p.snapshotBatch(ctx, syms, got)
		for sym, sn := range got {
			b := sn.GetBasic()
			row := wsmsg.WatchlistRow{Symbol: sym, Volume: b.GetVolume()}
			cur, lc := b.GetCurPrice(), b.GetLastClosePrice()
			row.Last = &cur
			if lc != 0 {
				cp := (cur - lc) / lc * 100
				row.ChangePct = &cp
			}
			p.rows[sym] = row
		}
		ref := p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
		p.lastRef = &ref
	}
	p.pub.Publish(wsmsg.TopicWatchlistRows, watchlistKey, p.buildPayload(syms))
}

// buildPayload assembles the snapshot: Symbols is always the full membership;
// Rows carries only symbols with a cached row (Symbols/Rows split is
// deliberate — membership is instantly correct, rows may lag).
func (p *Poller) buildPayload(syms []string) wsmsg.WatchlistRowsPayload {
	rows := make([]wsmsg.WatchlistRow, 0, len(syms))
	live := map[string]bool{}
	for _, s := range syms {
		live[s] = true
		if r, ok := p.rows[s]; ok {
			rows = append(rows, r)
		}
	}
	// Evict cache entries for removed symbols to bound memory.
	for s := range p.rows {
		if !live[s] {
			delete(p.rows, s)
		}
	}
	return wsmsg.WatchlistRowsPayload{RefreshedAt: p.lastRef, Symbols: syms, Rows: rows}
}

// snapshotBatch resolves one batch via a single 3203, recursing with a binary
// split on a whole-batch RetType != 0 failure (lifted from
// stockinfo.snapshotChunk / scan.snapshotBatch). Probe-at-add makes this a
// delisting/edge safety net, not a hot path.
func (p *Poller) snapshotBatch(ctx context.Context, syms []string, out map[string]*snappb.Snapshot) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: securitiesFor(syms)}})
	if err != nil {
		slog.Warn("watchlist: snapshot transport failed", "err", err, "n", len(syms))
		return
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("watchlist: snapshot decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		if len(syms) == 1 {
			slog.Info("watchlist: snapshot unresolvable this tick", "symbol", syms[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(syms) / 2
		p.snapshotBatch(ctx, syms[:mid], out)
		p.snapshotBatch(ctx, syms[mid:], out)
		return
	}
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		out[symbolOf(sn.GetBasic().GetSecurity())] = sn
	}
}

func securitiesFor(syms []string) []*qotcommon.Security {
	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	return secs
}

// codeOf/symbolOf replicate stockinfo.go:457-467 locally (the repo's
// established convention: each poller keeps its own copy rather than
// exporting these two one-liners from another package).
func codeOf(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode()
}
