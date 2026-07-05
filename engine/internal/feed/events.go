package feed

// Event is the sealed union of everything a Feed emits. Seed=true marks
// backfill-derived events (cache reads on subscribe/reconnect) — the md core
// applies them through the identical path as live events (dedup handles
// overlap), and the journal (Plan 3) records the flag.
type Event interface{ isEvent() }

// TicksEvent carries one push (or seed batch) of trade prints, oldest first.
type TicksEvent struct {
	Ticks []Tick
	Seed  bool
}

// QuoteEvent replaces the symbol's latest quote.
type QuoteEvent struct {
	Quote Quote
	Seed  bool
}

// BookEvent replaces the symbol's book.
type BookEvent struct {
	Book Book
	Seed bool
}

// Bars1mEvent carries authoritative 1m bars (K_1M push or cache seed),
// oldest first.
type Bars1mEvent struct {
	Bars []Bar
	Seed bool
}

// ConnUpEvent / ConnDownEvent are feed-connection transitions.
// ResyncedEvent fires after a reconnect once re-subscription and cache
// re-seeding are complete — consumers may re-snapshot.
type ConnUpEvent struct{}
type ConnDownEvent struct{}
type ResyncedEvent struct{}

func (TicksEvent) isEvent()    {}
func (QuoteEvent) isEvent()    {}
func (BookEvent) isEvent()     {}
func (Bars1mEvent) isEvent()   {}
func (ConnUpEvent) isEvent()   {}
func (ConnDownEvent) isEvent() {}
func (ResyncedEvent) isEvent() {}
