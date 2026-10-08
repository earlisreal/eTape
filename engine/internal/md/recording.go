package md

import (
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func (c *Core) record(r feed.Recording) {
	if c.cfg.Recorder == nil {
		return
	}
	if r.Source.Run == "" {
		r.Source.Run = c.cfg.Recorder.RunID()
	}
	if r.Source.ReceiptMs == 0 {
		r.Source.ReceiptMs = c.currentTime().UnixMilli()
	}
	if r.ProcessingOrdinal == 0 {
		r.ProcessingOrdinal = c.processingOrdinal
	}
	c.cfg.Recorder.Record(r)
}

func (c *Core) recordTick(t feed.Tick, accepted, tenLate, shadowLate bool) {
	if c.cfg.Recorder == nil {
		return
	}
	c.record(feed.Recording{Kind: "processing", Source: t.SourceRef(), Symbol: t.Symbol, TimeMs: t.TsMs, Sequence: t.Seq, Price: t.Price, Volume: t.Volume, ProcessingOrdinal: t.ProcessingOrdinal, Data: struct {
		Accepted, EligibilityStamped, Seed, RangeEligible, LastEligible, VolumeEligible, TenLate, ShadowLate bool
		Delivery                                                                                             feed.DeliverySource
	}{accepted, accepted, c.recordingSeed, t.RangeEligible && accepted, t.LastEligible && accepted, t.VolumeEligible && accepted, tenLate, shadowLate, t.Delivery}})
}

func (c *Core) recordClamp(original, trimmed, auth Bar, source feed.SourceRef, origin string) {
	if c.cfg.Recorder == nil {
		return
	}
	c.record(feed.Recording{Kind: "clamp", Source: source, Symbol: original.Symbol, TimeMs: original.BucketMs, Timeframe: string(session.TF10s), Data: struct {
		Before, After, Authoritative Bar
		Origin                       string
		SourceReferenceProvided      bool
	}{original, trimmed, auth, origin, source.Run != ""}})
}

type bucketBasis struct {
	Symbol                        string
	Timeframe                     session.Timeframe
	BucketMs                      int64
	HasAnchor                     bool
	Anchor                        float64
	AnchorTimeMs                  int64
	AnchorOrigin                  string
	AnchorSource                  feed.SourceRef
	HasRange                      bool
	High, Low                     feed.Tick
	HasLast                       bool
	First, Last                   feed.Tick
	Volume, BuyVolume, SellVolume int64
	VolumeReports                 int32
}

func (e *barEngine) rememberAuth(sb *symbolBars, raw feed.Bar) {
	if e.record == nil {
		return
	}
	sb.authSources[raw.BucketMs] = raw.Source
	if len(sb.authSources) > 1024 {
		oldest := raw.BucketMs
		for bucket := range sb.authSources {
			oldest = min(oldest, bucket)
		}
		delete(sb.authSources, oldest)
	}
}

func (a *tickAgg) recordBasis(b *tickBucket) {
	if a.record == nil {
		return
	}
	a.record(feed.Recording{Kind: "bucket_basis", Symbol: a.symbol, TimeMs: b.bucketMs, Timeframe: string(a.tf), Source: a.recordSource, Data: a.basisSnapshot(b)})
	b.basisDirty = false
}

func (a *tickAgg) basisSnapshot(b *tickBucket) bucketBasis {
	return bucketBasis{Symbol: a.symbol, Timeframe: a.tf, BucketMs: b.bucketMs, HasAnchor: b.hasAnchor, Anchor: b.anchor, AnchorTimeMs: b.anchorTime, AnchorOrigin: b.anchorOrigin, AnchorSource: b.anchorSource, HasRange: b.hasRange, High: b.highReport, Low: b.lowReport, HasLast: b.hasLast, First: b.firstReport, Last: b.lastReport, Volume: b.v, BuyVolume: b.buyV, SellVolume: b.sellV, VolumeReports: b.ticks}
}

func (c *Core) recordOpenBases() {
	if c.cfg.Recorder == nil {
		return
	}
	for _, sb := range c.bars.symbols {
		for _, a := range []*tickAgg{sb.agg10, sb.shadow} {
			for _, b := range a.open {
				if b.basisDirty {
					a.recordBasis(b)
				}
			}
		}
	}
}
