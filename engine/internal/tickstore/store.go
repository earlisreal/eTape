// Package tickstore retains immutable market-data evidence independently of
// execution persistence. Its SQLite files are the queryable archive interface.
package tickstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/buildinfo"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/singleinstance"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE tickstore_meta (owner TEXT NOT NULL, run_id TEXT NOT NULL,
 schema_version INTEGER NOT NULL, policy_version TEXT NOT NULL, created_ms INTEGER NOT NULL,
 closed_ms INTEGER, clean INTEGER NOT NULL DEFAULT 0, last_commit_ms INTEGER,
 attribution_incomplete INTEGER NOT NULL DEFAULT 0, configuration TEXT NOT NULL, build TEXT NOT NULL,
 last_ingress INTEGER NOT NULL DEFAULT 0, last_processing INTEGER NOT NULL DEFAULT 0);
CREATE TABLE observations (id INTEGER PRIMARY KEY, kind TEXT NOT NULL,
 run_id TEXT NOT NULL, connection INTEGER NOT NULL, ingress INTEGER NOT NULL,
 list_index INTEGER NOT NULL, receipt_ms INTEGER NOT NULL, symbol TEXT NOT NULL, timeframe TEXT NOT NULL,
 time_ms INTEGER NOT NULL, sequence INTEGER NOT NULL, price REAL NOT NULL,
 volume INTEGER NOT NULL, direction INTEGER NOT NULL, condition INTEGER NOT NULL,
 processing_ordinal INTEGER NOT NULL, observation_ms INTEGER NOT NULL, body BLOB, data TEXT NOT NULL);
CREATE INDEX observation_exchange ON observations(kind,symbol,timeframe,time_ms);
CREATE INDEX observation_source ON observations(run_id,connection,ingress,list_index,kind);
CREATE INDEX observation_receipt ON observations(kind,symbol,receipt_ms,ingress);
CREATE INDEX observation_processing ON observations(processing_ordinal) WHERE kind='processing';
CREATE TABLE active_basis (basis_key TEXT PRIMARY KEY, data TEXT NOT NULL);
`

const insertObservation = `INSERT INTO observations
 (kind,run_id,connection,ingress,list_index,receipt_ms,symbol,timeframe,time_ms,sequence,
 price,volume,direction,condition,processing_ordinal,observation_ms,body,data) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// Options contains bootstrap settings and system seams used by fixture/load tests.
// Decode runs on the writer, never on the socket, feed, or MD goroutine.
type Options struct {
	Directory     string
	RetentionDays int
	MaxBytes      int64
	MinFreeBytes  uint64
	SegmentBytes  int64
	QueueBytes    int64
	QueueItems    int
	FlushInterval time.Duration
	AnchorSecs    int64
	Now           func() time.Time
	FreeSpace     func(string) (uint64, error)
	Decode        func(feed.Recording) ([]feed.Recording, error)
	Health        func(error)
}

type queued struct {
	record feed.Recording
	bytes  int64
}

// Stats is a race-safe operational snapshot; committed latency excludes losses.
type Stats struct {
	WorkingBytes   int64
	WorkingItems   int64
	Committed      uint64
	Lost           [feed.RecordingLaneCount]uint64
	MaxCommitLagMs int64
	Paused         bool
}

type Store struct {
	opt         Options
	run         string
	started     time.Time
	release     func() error
	db          *sql.DB
	path        string
	day         string
	segment     uint64
	queue       chan queued
	wake        chan struct{}
	stop        chan struct{}
	done        chan struct{}
	gate        sync.Mutex
	closed      bool
	closing     atomic.Bool
	submissions atomic.Int64
	closeCtx    context.Context
	closeErr    error // read only after done closes
	bytes       atomic.Int64
	items       atomic.Int64
	committed   atomic.Uint64
	lag         atomic.Int64
	paused      atomic.Bool
	loss        [feed.RecordingLaneCount]atomic.Uint64
	totalLoss   [feed.RecordingLaneCount]atomic.Uint64
	firstLoss   [feed.RecordingLaneCount]atomic.Pointer[feed.SourceRef]
	lastLoss    [feed.RecordingLaneCount]atomic.Pointer[feed.SourceRef]
	basisWork   atomic.Int64
	basis       map[string]string
	segments    map[string]int64
	basisBytes  int64
	incomplete  bool
	lastFailure error
	recovered   []string
}

func Open(o Options) (*Store, error) {
	if o.Directory == "" {
		return nil, errors.New("tickstore: directory required")
	}
	if o.RetentionDays == 0 {
		o.RetentionDays = 30
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 10 << 30
	}
	if o.MinFreeBytes == 0 {
		o.MinFreeBytes = 2 << 30
	}
	if o.SegmentBytes == 0 {
		o.SegmentBytes = 256 << 20
	}
	if o.QueueBytes == 0 {
		o.QueueBytes = 32 << 20
	}
	if o.QueueItems == 0 {
		o.QueueItems = 4096
	}
	if o.FlushInterval == 0 {
		o.FlushInterval = 250 * time.Millisecond
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.FreeSpace == nil {
		o.FreeSpace = freeSpace
	}
	if o.RetentionDays < 1 || o.MaxBytes < 2<<20 || o.SegmentBytes < 1 || o.QueueBytes < 4096 || o.QueueItems < 1 || o.FlushInterval <= 0 {
		return nil, errors.New("tickstore: invalid recording limits")
	}
	root, err := filepath.Abs(o.Directory)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	o.Directory = root
	release, err := singleinstance.Acquire(filepath.Join(root, "recorder.lock"))
	if err != nil {
		return nil, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		_ = release()
		return nil, err
	}
	s := &Store{opt: o, run: hex.EncodeToString(token[:]), started: o.Now(), release: release, queue: make(chan queued, o.QueueItems), wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), basis: make(map[string]string), segments: make(map[string]int64)}
	if err = s.recoverRun(); err != nil {
		_ = release()
		return nil, err
	}
	if err = s.recoverSegments(); err != nil {
		_ = release()
		return nil, err
	}
	if err = s.newSegment(); err != nil {
		_ = release()
		return nil, err
	}
	if err = s.writeRun(true); err != nil {
		_ = s.db.Close()
		_ = release()
		return nil, err
	}
	go s.runWriter()
	for _, prior := range s.recovered {
		s.Record(feed.Recording{Kind: "restart_gap", Data: struct{ PreviousRun, Reason string }{prior, "previous run did not close cleanly; uncommitted tail unknown"}})
	}
	return s, nil
}

func (s *Store) RunID() string { return s.run }

func lane(kind string) feed.RecordingLane {
	switch kind {
	case "source", "print", "bbo", "minute":
		return feed.RecordingSource
	case "routing":
		return feed.RecordingRouting
	case "processing", "processing_stop", "bucket_basis", "bucket_final", "clamp", "basis_remove":
		return feed.RecordingProcessing
	default:
		return feed.RecordingCoverage
	}
}

// Record reserves decoded/indexed work too. Atomic reservations and the bounded
// channel never wait for writer or filesystem work.
func (s *Store) Record(r feed.Recording) bool {
	s.submissions.Add(1)
	defer s.submissions.Add(-1)
	cost := int64(len(r.Body))*128 + 4096
	if s.closing.Load() || s.paused.Load() || cost > s.opt.QueueBytes*3/4 {
		s.Lost(lane(r.Kind), r.Source, 1)
		return false
	}
	for {
		before := s.bytes.Load()
		if cost > s.opt.QueueBytes*3/4-before {
			s.Lost(lane(r.Kind), r.Source, 1)
			return false
		}
		if s.bytes.CompareAndSwap(before, before+cost) {
			break
		}
	}
	for {
		before := s.items.Load()
		if before >= int64(s.opt.QueueItems) {
			s.bytes.Add(-cost)
			s.Lost(lane(r.Kind), r.Source, 1)
			return false
		}
		if s.items.CompareAndSwap(before, before+1) {
			break
		}
	}
	if r.Source.Run == "" {
		r.Source.Run = s.run
	}
	if r.Source.ReceiptMs == 0 {
		r.Source.ReceiptMs = s.opt.Now().UnixMilli()
	}
	if r.ObservationMs == 0 {
		r.ObservationMs = s.opt.Now().UnixMilli()
	}
	select {
	case s.queue <- queued{r, cost}:
		if len(s.queue) >= 128 {
			s.signalWriter()
		}
		return true
	default:
		s.bytes.Add(-cost)
		s.items.Add(-1)
		s.Lost(lane(r.Kind), r.Source, 1)
		return false
	}
}

func (s *Store) signalWriter() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Lost survives a saturated queue. Interval bounds are deliberately conservative
// (run start through marker commit); only these measured item counts are exact.
func (s *Store) Lost(l feed.RecordingLane, ref feed.SourceRef, count uint64) {
	if l >= feed.RecordingLaneCount {
		l = feed.RecordingCoverage
	}
	s.loss[l].Add(count)
	s.totalLoss[l].Add(count)
	if ref.Ingress != 0 {
		copy := ref
		s.firstLoss[l].CompareAndSwap(nil, &copy)
		s.lastLoss[l].Store(&copy)
	}
}

func (s *Store) Stats() Stats {
	out := Stats{WorkingBytes: s.bytes.Load() + s.basisWork.Load(), WorkingItems: s.items.Load(), Committed: s.committed.Load(), MaxCommitLagMs: s.lag.Load(), Paused: s.paused.Load()}
	for i := range s.totalLoss {
		out.Lost[i] = s.totalLoss[i].Load()
	}
	return out
}

// Close is called only after all producers have joined. A timeout leaves the
// writer finishing asynchronously and reports an incomplete tail.
func (s *Store) Close(ctx context.Context) error {
	s.gate.Lock()
	if !s.closed {
		s.closed = true
		s.closing.Store(true)
		s.closeCtx = ctx
		close(s.stop)
	}
	s.gate.Unlock()
	select {
	case <-s.done:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) runWriter() {
	defer close(s.done)
	defer func() {
		if s.db != nil {
			s.closeErr = errors.Join(s.closeErr, s.db.Close())
		}
		s.closeErr = errors.Join(s.closeErr, s.release())
	}()
	tick := time.NewTicker(s.opt.FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			s.flush()
		case <-s.wake:
			s.flush()
		case <-s.stop:
			for s.submissions.Load() > 0 && s.closeCtx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			s.flush()
			for s.items.Load() > 0 && !s.paused.Load() && s.closeCtx.Err() == nil {
				s.flush()
			}
			if s.items.Load() != 0 || s.paused.Load() || s.closeCtx.Err() != nil {
				s.closeErr = errors.Join(errors.New("tickstore: incomplete recording tail"), s.lastFailure, s.closeCtx.Err())
				return
			}
			s.closeErr = s.seal(true)
			if s.closeErr == nil {
				s.closeErr = s.writeRun(false)
			}
			return
		}
		if len(s.queue) >= 128 {
			s.signalWriter()
		}
	}
}

type encoded struct {
	record feed.Recording
	data   string
}

func (s *Store) flush() {
	var input []queued
	for len(input) < 128 {
		select {
		case q := <-s.queue:
			input = append(input, q)
		default:
			goto drained
		}
	}
drained:
	var loss [feed.RecordingLaneCount]uint64
	for i := range loss {
		loss[i] = s.loss[i].Swap(0)
	}
	var records []feed.Recording
	for _, q := range input {
		r := q.record
		if r.Kind == "source" && s.opt.Decode != nil {
			decoded, err := s.opt.Decode(r)
			if err != nil {
				s.incomplete = true
				records = append(records, feed.Recording{Kind: "decode_gap", Source: r.Source, Data: struct{ Error string }{err.Error()}})
			}
			records = append(records, decoded...)
		} else {
			records = append(records, r)
		}
	}
	for l, count := range loss {
		if count > 0 {
			s.incomplete = true
			records = append(records, feed.Recording{Kind: "capture_gap", Source: feed.SourceRef{Run: s.run, ReceiptMs: s.opt.Now().UnixMilli()}, Data: struct {
				Lane                  int
				Count                 uint64
				FromMs, ThroughMs     int64
				Bounds                string
				FirstKnown, LastKnown *feed.SourceRef
			}{l, count, s.started.UnixMilli(), s.opt.Now().UnixMilli(), "conservative run interval; known references are examples, not continuous ranges", s.firstLoss[l].Load(), s.lastLoss[l].Load()}})
		}
	}
	if len(records) == 0 && !s.paused.Load() {
		return
	}
	var encodedRows []encoded
	var growth int64 = 2 << 20
	for _, r := range records {
		data, err := json.Marshal(r.Data)
		if err != nil || len(data) > 64<<10 {
			s.incomplete = true
			// Preserve the raw source group even when a malformed indexed
			// value (for example NaN) cannot be represented as JSON/SQLite.
			if r.Kind != "source" {
				r.Kind = "index_gap"
				r.Body = nil
			}
			r.Price = 0
			data = []byte(`{"Reason":"invalid or oversized indexed value; inspect original source payload"}`)
		}
		growth += int64(len(data)+len(r.Body))*8 + 128<<10
		encodedRows = append(encodedRows, encoded{r, string(data)})
	}
	err := s.ensureBudget(growth)
	if err == nil && s.path != "" {
		size, statErr := segmentSize(s.path)
		if statErr != nil {
			err = statErr
		} else if s.day != s.opt.Now().In(session.Loc()).Format("20060102") || size >= s.opt.SegmentBytes {
			if err = s.seal(true); err == nil {
				err = s.newSegment()
			}
		}
	}
	if err == nil && s.db == nil {
		err = s.newSegment()
	}
	if err == nil {
		err = s.commit(encodedRows)
	}
	if err != nil {
		s.incomplete = true
		for _, q := range input {
			s.Lost(lane(q.record.Kind), q.record.Source, 1)
		}
		for l, count := range loss {
			s.loss[l].Add(count)
		}
		s.lastFailure = err
		if !s.paused.Swap(true) && s.opt.Health != nil {
			s.opt.Health(err)
		}
	} else {
		s.lastFailure = nil
		if s.paused.Swap(false) && s.opt.Health != nil {
			s.opt.Health(nil)
		}
	}
	for _, q := range input {
		s.bytes.Add(-q.bytes)
		s.items.Add(-1)
	}
}

func (s *Store) commit(rows []encoded) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, insertObservation)
	if err != nil {
		return err
	}
	defer stmt.Close()
	basis := maps.Clone(s.basis)
	basisBytes := s.basisBytes
	incomplete := s.incomplete
	for _, row := range rows {
		r := row.record
		if r.Kind == "bucket_basis" {
			key := fmt.Sprintf("%s/%s/%d", r.Symbol, r.Timeframe, r.TimeMs)
			old := basis[key]
			if basisBytes-int64(len(old))+int64(len(row.data)) <= s.opt.QueueBytes/4 {
				if _, err = tx.ExecContext(ctx, "INSERT INTO active_basis VALUES (?,?) ON CONFLICT(basis_key) DO UPDATE SET data=excluded.data", key, row.data); err != nil {
					return err
				}
				basisBytes += int64(len(row.data) - len(old))
				basis[key] = row.data
			} else {
				incomplete = true
			}
		}
		if r.Kind == "bucket_final" {
			key := fmt.Sprintf("%s/%s/%d", r.Symbol, r.Timeframe, r.TimeMs)
			if _, err = tx.ExecContext(ctx, "DELETE FROM active_basis WHERE basis_key=?", key); err != nil {
				return err
			}
			basisBytes -= int64(len(basis[key]))
			delete(basis, key)
		}
		if r.ObservationMs == 0 {
			r.ObservationMs = s.opt.Now().UnixMilli()
		}
		if _, err = stmt.ExecContext(ctx, r.Kind, r.Source.Run, r.Source.Connection, r.Source.Ingress, r.Source.Index, r.Source.ReceiptMs, r.Symbol, r.Timeframe, r.TimeMs, r.Sequence, r.Price, r.Volume, r.Direction, r.Condition, r.ProcessingOrdinal, r.ObservationMs, r.Body, row.data); err != nil {
			return err
		}
	}
	var ingress, processing uint64
	for _, row := range rows {
		ingress = max(ingress, row.record.Source.Ingress)
		processing = max(processing, row.record.ProcessingOrdinal)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tickstore_meta SET last_commit_ms=?,attribution_incomplete=?,last_ingress=MAX(last_ingress,?),last_processing=MAX(last_processing,?)", s.opt.Now().UnixMilli(), incomplete, ingress, processing); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.basis, s.basisBytes, s.incomplete = basis, basisBytes, incomplete
	for _, row := range rows {
		r := row.record
		from := r.ObservationMs
		switch r.Kind {
		case "source", "print", "bbo", "minute":
			from = r.Source.ReceiptMs
		}
		if from == 0 {
			continue
		}
		lag := s.opt.Now().UnixMilli() - from
		for lag > s.lag.Load() {
			prior := s.lag.Load()
			if lag <= prior || s.lag.CompareAndSwap(prior, lag) {
				break
			}
		}
	}
	s.committed.Add(uint64(len(rows)))
	s.basisWork.Store(s.basisBytes)
	return nil
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=50; PRAGMA synchronous=FULL; PRAGMA temp_store=MEMORY;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Store) newSegment() error {
	if err := s.ensureBudget(2<<20 + int64(len(s.basis))*128<<10 + s.basisBytes*8); err != nil {
		return err
	}
	s.segment++
	s.day = s.opt.Now().In(session.Loc()).Format("20060102")
	path := filepath.Join(s.opt.Directory, fmt.Sprintf("tick-%s-%s-%06d.sqlite", s.day, s.run, s.segment))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	db, err := openDB(path)
	if err != nil {
		return err
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL;"); err == nil {
		_, err = db.Exec(schema)
	}
	configuration, _ := json.Marshal(struct {
		RetentionDays int
		MaxBytes      int64
		MinFreeBytes  uint64
		AnchorSecs    int64
	}{s.opt.RetentionDays, s.opt.MaxBytes, s.opt.MinFreeBytes, s.opt.AnchorSecs})
	build := "engine=" + buildinfo.Version
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision, modified string
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" {
				modified = setting.Value
			}
		}
		build += fmt.Sprintf(" go=%s module=%s revision=%s modified=%s", info.GoVersion, info.Main.Version, revision, modified)
	}
	if err == nil {
		_, err = db.Exec("INSERT INTO tickstore_meta(owner,run_id,schema_version,policy_version,created_ms,configuration,build,attribution_incomplete) VALUES ('etape.tickstore',?,1,'trade-report-eligibility-v1',?,?,?,?)", s.run, s.opt.Now().UnixMilli(), string(configuration), build, s.incomplete)
	}
	if err == nil {
		for key, data := range s.basis {
			if _, err = db.Exec("INSERT INTO active_basis VALUES (?,?)", key, data); err != nil {
				break
			}
		}
	}
	if err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	s.path = path
	s.segments[path] = s.opt.Now().UnixMilli()
	return nil
}

func checkpoint(db *sql.DB) error {
	var busy, total, done int
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &total, &done); err != nil {
		return err
	}
	if busy != 0 || total != done {
		return errors.New("tickstore: reader prevents checkpoint")
	}
	return nil
}

func (s *Store) seal(clean bool) error {
	if s.db == nil {
		return nil
	}
	info, err := os.Stat(s.path + "-wal")
	var reserve int64 = 128 << 10
	if err == nil {
		reserve += info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = s.ensureBudget(reserve); err != nil {
		return err
	}
	if err = checkpoint(s.db); err != nil {
		return err
	}
	if _, err = s.db.Exec("UPDATE tickstore_meta SET clean=?,closed_ms=?", clean, s.opt.Now().UnixMilli()); err != nil {
		return err
	}
	if err = checkpoint(s.db); err != nil {
		return err
	}
	if err = s.db.Close(); err != nil {
		return err
	}
	s.db = nil
	s.path = ""
	return nil
}
