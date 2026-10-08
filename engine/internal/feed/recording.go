package feed

// SourceRef identifies a delivery, independently of asynchronous database rows.
// Index is the original protobuf list position, before cache sorting.
type SourceRef struct {
	Run        string
	Connection uint64
	Ingress    uint64
	Index      int32
	ReceiptMs  int64
}

// Recording is immutable after submission. Data contains fixed observation
// values; source bodies are decoded/indexed only on the archive writer.
type Recording struct {
	Kind              string
	Source            SourceRef
	Symbol            string
	Timeframe         string
	TimeMs            int64
	Sequence          int64
	Price             float64
	Volume            int64
	Direction         int32
	Condition         int32
	ProcessingOrdinal uint64
	ObservationMs     int64
	Body              []byte
	Data              any
}

// RecordingLane keeps loss accounting independent when the data queue is full.
type RecordingLane uint8

const (
	RecordingSource RecordingLane = iota
	RecordingRouting
	RecordingProcessing
	RecordingCoverage
	RecordingLaneCount
)

// Recorder never waits for queue space or performs I/O on its callers.
type Recorder interface {
	RunID() string
	Record(Recording) bool
	Lost(RecordingLane, SourceRef, uint64)
}

// SourceMessage carries only market-data request context, never credentials.
type SourceMessage struct {
	Protocol   uint32
	Version    uint8
	Format     uint8
	Serial     uint32
	Origin     string
	Symbol     string
	Timeframe  int32
	Adjustment int32
	Matched    bool
}

// EventSource identifies the first report in a normalized event; it does not
// imply that every report in a cache batch has contiguous source indexes.
func EventSource(ev Event) SourceRef {
	switch e := ev.(type) {
	case TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].SourceRef()
		}
	case Bars1mEvent:
		if len(e.Bars) > 0 {
			return e.Bars[0].Source
		}
	case BookEvent:
		return e.Book.Source
	case QuoteEvent:
		return e.Quote.Source
	}
	return SourceRef{}
}

// SourceRef returns immutable optional provenance without expanding every
// tape ring slot by the full identity when recording is disabled.
func (t Tick) SourceRef() SourceRef {
	if t.Source != nil {
		return *t.Source
	}
	return SourceRef{}
}
