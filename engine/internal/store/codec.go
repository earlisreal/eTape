package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	kindTicks    = "ticks"
	kindQuote    = "quote"
	kindBook     = "book"
	kindBars1m   = "bars1m"
	kindConnUp   = "conn_up"
	kindConnDown = "conn_down"
	kindResynced = "resynced"
)

// eventKind returns the journal `kind` discriminator for ev.
func eventKind(ev feed.Event) string {
	switch ev.(type) {
	case feed.TicksEvent:
		return kindTicks
	case feed.QuoteEvent:
		return kindQuote
	case feed.BookEvent:
		return kindBook
	case feed.Bars1mEvent:
		return kindBars1m
	case feed.ConnUpEvent:
		return kindConnUp
	case feed.ConnDownEvent:
		return kindConnDown
	case feed.ResyncedEvent:
		return kindResynced
	default:
		return ""
	}
}

// eventSeed reports the Seed flag (false for conn/resync events).
func eventSeed(ev feed.Event) bool {
	switch e := ev.(type) {
	case feed.TicksEvent:
		return e.Seed
	case feed.QuoteEvent:
		return e.Seed
	case feed.BookEvent:
		return e.Seed
	case feed.Bars1mEvent:
		return e.Seed
	default:
		return false
	}
}

// eventSymbol returns the primary symbol, or "" (conn/resync, empty batch).
func eventSymbol(ev feed.Event) string {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].Symbol
		}
	case feed.QuoteEvent:
		return e.Quote.Symbol
	case feed.BookEvent:
		return e.Book.Symbol
	case feed.Bars1mEvent:
		if len(e.Bars) > 0 {
			return e.Bars[0].Symbol
		}
	}
	return ""
}

// eventExchTs returns the primary exchange ts (ms), or fallback when the event
// carries none (conn/resync, empty batch).
func eventExchTs(ev feed.Event, fallback int64) int64 {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].TsMs
		}
	case feed.QuoteEvent:
		return e.Quote.TsMs
	case feed.BookEvent:
		return e.Book.TsMs
	case feed.Bars1mEvent:
		if len(e.Bars) > 0 {
			return e.Bars[0].BucketMs
		}
	}
	return fallback
}

// encodePayload marshals the whole event struct. No struct tags exist on the
// feed types, so JSON keys are the exported Go field names — stable and lossless
// (Go's json round-trips float64 exactly).
func encodePayload(ev feed.Event) ([]byte, error) {
	return json.Marshal(ev)
}

// decodePayload reconstructs a feed.Event: kind selects the concrete type,
// json.Unmarshal fills it (including the Seed flag inside the payload).
func decodePayload(kind string, payload []byte) (feed.Event, error) {
	switch kind {
	case kindTicks:
		var v feed.TicksEvent
		return v, json.Unmarshal(payload, &v)
	case kindQuote:
		var v feed.QuoteEvent
		return v, json.Unmarshal(payload, &v)
	case kindBook:
		var v feed.BookEvent
		return v, json.Unmarshal(payload, &v)
	case kindBars1m:
		var v feed.Bars1mEvent
		return v, json.Unmarshal(payload, &v)
	case kindConnUp:
		return feed.ConnUpEvent{}, nil
	case kindConnDown:
		return feed.ConnDownEvent{}, nil
	case kindResynced:
		return feed.ResyncedEvent{}, nil
	default:
		return nil, fmt.Errorf("store: unknown journal kind %q", kind)
	}
}

// dayKey formats the ET trading day ("YYYY-MM-DD") containing a ms timestamp.
func dayKey(tsMs int64) string {
	return time.UnixMilli(session.DayMs(tsMs)).In(session.Loc()).Format("2006-01-02")
}
