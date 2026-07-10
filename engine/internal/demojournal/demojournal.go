// Package demojournal writes a deterministic synthetic trading day into a SQLite
// journal, replayable by `etape -replay <day>` — no OpenD, no market hours,
// byte-for-byte reproducible.
package demojournal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

var symbols = []struct {
	sym  string
	open float64
}{
	{"US.AAPL", 190.00},
	{"US.NVDA", 140.00},
}

const (
	bars    = 20 // 1m bars per symbol
	ticksPM = 6  // ticks per minute per symbol
)

// Generate writes a deterministic synthetic trading day for `day` (YYYY-MM-DD,
// ET) into the SQLite journal at dbPath, creating parent directories as needed.
func Generate(dbPath, day string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	st, err := store.Open(store.Options{Path: dbPath, Clock: clock.System{}})
	if err != nil {
		return err
	}

	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		st.Close()
		return fmt.Errorf("bad -day %q: %w", day, err)
	}
	// Anchor at 09:30:00 ET so dayKey(recvMs) resolves to `day` and the bars
	// read as RTH for session shading.
	base := time.Date(d.Year(), d.Month(), d.Day(), 9, 30, 0, 0, session.Loc())

	for _, s := range symbols {
		px := s.open
		openMs := base.UnixMilli()
		st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{
			Symbol: s.sym, TsMs: openMs, Last: px, Open: px, High: px, Low: px,
			PrevClose: px, Volume: 0, Turnover: 0,
		}}, openMs)
		st.RecordEvent(feed.BookEvent{Book: bookAround(s.sym, openMs, px)}, openMs)

		for m := 0; m < bars; m++ {
			minStart := base.Add(time.Duration(m) * time.Minute)
			o := px
			hi, lo := px, px
			for k := 0; k < ticksPM; k++ {
				px += 0.05
				if k == ticksPM-1 {
					px -= 0.03
				}
				if px > hi {
					hi = px
				}
				if px < lo {
					lo = px
				}
				tickMs := minStart.Add(time.Duration(k) * 10 * time.Second).UnixMilli()
				dir := feed.Buy
				if k%2 == 1 {
					dir = feed.Sell
				}
				st.RecordEvent(feed.TicksEvent{Ticks: []feed.Tick{{
					Symbol: s.sym, Seq: int64(m*ticksPM + k), TsMs: tickMs,
					Price: round2(px), Volume: 100, Turnover: round2(px) * 100, Dir: dir,
				}}}, tickMs)
			}
			barMs := minStart.UnixMilli()
			st.RecordEvent(feed.Bars1mEvent{Bars: []feed.Bar{{
				Symbol: s.sym, BucketMs: barMs,
				O: round2(o), H: round2(hi), L: round2(lo), C: round2(px),
				Volume: int64(ticksPM * 100), Turnover: round2(px) * float64(ticksPM*100),
			}}}, barMs)
		}
	}

	return st.Close() // flushes the writer queue
}

func bookAround(sym string, tsMs int64, px float64) feed.Book {
	var bids, asks []feed.BookLevel
	for i := 1; i <= 5; i++ {
		bids = append(bids, feed.BookLevel{Price: round2(px - 0.01*float64(i)), Volume: int64(100 * i), Orders: int32(i)})
		asks = append(asks, feed.BookLevel{Price: round2(px + 0.01*float64(i)), Volume: int64(100 * i), Orders: int32(i)})
	}
	return feed.Book{Symbol: sym, TsMs: tsMs, Bids: bids, Asks: asks}
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
