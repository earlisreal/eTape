package cryptoscanner

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func TestSnapshotRowsKeepsFreshCataloguedMetrics(t *testing.T) {
	now := time.Unix(1800000000, 0)
	markets := []instrument.PerpetualMarket{
		{ID: instrument.Perpetual{Exchange: "MEXC", Native: "BTC_USDT"}, CryptoUnderlying: true},
		{ID: instrument.Perpetual{Exchange: "MEXC", Native: "ETH_USDT"}, CryptoUnderlying: true},
		{ID: instrument.Perpetual{Exchange: "MEXC", Native: "GOLD_USDT"}},
	}
	zero, negative := 0.0, -3.25
	rows := snapshotRows(now, markets, []Ticker{
		{Symbol: "BTC_USDT", Turnover: &zero, ReportedChangePct: &zero, Timestamp: now.UnixMilli()},
		{Symbol: "ETH_USDT", ReportedChangePct: &zero, Timestamp: now.Add(-21 * time.Second).UnixMilli()},
		{Symbol: "GOLD_USDT", Turnover: &negative, ReportedChangePct: &negative, Timestamp: now.UnixMilli()},
		{Symbol: "UNKNOWN_USDT", Turnover: &zero, Timestamp: now.UnixMilli()},
	}).Rows
	if len(rows) != 2 || rows[0].Symbol != "PERP.MEXC.BTC_USDT" || rows[1].Symbol != "PERP.MEXC.GOLD_USDT" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].Last != nil || rows[0].Turnover == nil || *rows[0].Turnover != 0 ||
		rows[0].ReportedChangePct == nil || *rows[0].ReportedChangePct != 0 ||
		rows[1].Turnover != nil || rows[1].ReportedChangePct == nil || *rows[1].ReportedChangePct != negative {
		t.Fatalf("unexpected metrics: %+v", rows)
	}
}
