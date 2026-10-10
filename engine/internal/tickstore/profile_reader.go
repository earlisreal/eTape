package tickstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/singleinstance"
)

var ErrProfileBusy = errors.New("volume profile reader busy")

type profileBoundary struct{ ID, AsOfMs int64 }
type profileCursor struct{ Time, ID int64 }
type profileSnapshot struct{ segments map[string]profileBoundary }

func (s *Store) publishProfileSnapshot() {
	s.profileSnapshot.Store(&profileSnapshot{segments: maps.Clone(s.profileBounds)})
}

type ProfileRead struct {
	Prints                            []feed.Tick
	Reasons                           []string
	AsOfMs, FirstPrintMs, LastPrintMs int64
}

// ProfileReader owns no persistent SQLite handles or writer connection. Each
// indexed chunk releases its read lock and handles before maintenance proceeds.
type ProfileReader struct {
	directory string
	owner     *Store
	reads     chan struct{}
}

func NewProfileReader(directory string, owner *Store) *ProfileReader {
	return &ProfileReader{directory: directory, owner: owner, reads: make(chan struct{}, 2)}
}

func (r *ProfileReader) Read(ctx context.Context, symbol string, from, to int64) (ProfileRead, error) {
	out := ProfileRead{Prints: []feed.Tick{}, Reasons: []string{"coverage_unproven"}}
	if from <= 0 || to <= from || to > 253402300799999 || symbol == "" {
		return out, errors.New("invalid profile range")
	}
	select {
	case r.reads <- struct{}{}:
		defer func() { <-r.reads }()
	default:
		return out, ErrProfileBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	var snapshot *profileSnapshot
	if r.owner != nil {
		if r.owner.closing.Load() {
			return out, ErrProfileBusy
		}
		snapshot = r.owner.profileSnapshot.Load()
	} else {
		root, err := filepath.Abs(r.directory)
		if err != nil {
			return out, err
		}
		root, err = filepath.EvalSymlinks(root)
		if errors.Is(err, os.ErrNotExist) {
			out.Reasons = append(out.Reasons, "missing_capture")
			return out, nil
		}
		if err != nil {
			return out, err
		}
		release, err := singleinstance.Acquire(filepath.Join(root, "recorder.lock"))
		if err != nil {
			return out, ErrProfileBusy
		}
		defer func() { _ = release() }()
		entries, err := os.ReadDir(root)
		if err != nil {
			return out, err
		}
		snapshot = &profileSnapshot{segments: make(map[string]profileBoundary)}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if !segmentName.MatchString(entry.Name()) || !entry.Type().IsRegular() {
				continue
			}
			path := filepath.Join(root, entry.Name())
			db, err := r.openReadDB(path)
			if err != nil {
				out.addReason("unreadable_segment")
				continue
			}
			boundary, _, err := readProfileMetadata(ctx, db, path, true)
			_ = db.Close()
			if err != nil {
				out.addReason("uncoordinated_segment")
				continue
			}
			snapshot.segments[path] = boundary
		}
	}
	if snapshot == nil || len(snapshot.segments) == 0 {
		out.addReason("missing_capture")
		return out, nil
	}
	paths := slices.Sorted(maps.Keys(snapshot.segments))
	dayFrom := session.DayMs(from)
	lastDay := time.UnixMilli(session.DayMs(to - 1)).In(session.Loc())
	dayTo := lastDay.AddDate(0, 0, 1).UnixMilli()
	for _, path := range paths {
		boundary := snapshot.segments[path]
		out.AsOfMs = max(out.AsOfMs, boundary.AsOfMs)
		cursor := profileCursor{Time: dayFrom}
		for {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			count, err := r.readChunk(ctx, path, boundary.ID, symbol, dayFrom, dayTo, &cursor, &out)
			if err != nil {
				if errors.Is(err, md.ErrVolumeProfileTooLarge) || errors.Is(err, ErrProfileBusy) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return out, err
				}
				out.addReason("unreadable_segment")
				break
			}
			if count == 0 {
				break
			}
			if count < 1024 {
				break
			}
		}
	}
	for _, tick := range out.Prints {
		if tick.TsMs >= from && tick.TsMs < to {
			if out.FirstPrintMs == 0 || tick.TsMs < out.FirstPrintMs {
				out.FirstPrintMs = tick.TsMs
			}
			out.LastPrintMs = max(out.LastPrintMs, tick.TsMs)
		}
	}
	slices.Sort(out.Reasons)
	return out, nil
}

func (p *ProfileRead) addReason(reason string) {
	if !slices.Contains(p.Reasons, reason) {
		p.Reasons = append(p.Reasons, reason)
	}
}

func (r *ProfileReader) openReadDB(path string) (*sql.DB, error) {
	if _, err := segmentSize(path); err != nil {
		return nil, err
	}
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	u := url.URL{Scheme: "file", Path: name}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "cache_size(-1024)", "busy_timeout(0)"}}
	if r.owner == nil {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func readProfileMetadata(ctx context.Context, db *sql.DB, path string, sealed bool) (profileBoundary, bool, error) {
	var owner, run string
	var version, clean, incomplete int
	var b profileBoundary
	err := db.QueryRowContext(ctx, "SELECT owner,run_id,schema_version,clean,attribution_incomplete,COALESCE(last_commit_ms,0) FROM tickstore_meta").Scan(&owner, &run, &version, &clean, &incomplete, &b.AsOfMs)
	if err != nil {
		return b, false, err
	}
	if owner != "etape.tickstore" || version != 1 || !strings.Contains(filepath.Base(path), "-"+run+"-") || (sealed && clean != 1) {
		return b, false, errors.New("unowned or unsealed archive segment")
	}
	err = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM observations").Scan(&b.ID)
	return b, incomplete != 0, err
}

func (r *ProfileReader) readChunk(ctx context.Context, path string, maxID int64, symbol string, from, to int64, cursor *profileCursor, out *ProfileRead) (int, error) {
	if r.owner != nil {
		// Avoid permanent timer-phase collisions without opening a handle or
		// delaying the writer while maintenance owns the gate.
		deadline := time.Now().Add(250 * time.Millisecond)
		for !r.owner.profileGate.TryRLock() {
			if time.Now().After(deadline) {
				return 0, ErrProfileBusy
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
		defer r.owner.profileGate.RUnlock()
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	db, err := r.openReadDB(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	_, incomplete, err := readProfileMetadata(ctx, db, path, r.owner == nil)
	if err != nil {
		return 0, err
	}
	if incomplete {
		out.addReason("archive_incomplete")
	}
	if cursor.ID == 0 {
		var gap bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM observations WHERE kind IN ('capture_gap','decode_gap','request_gap','transport_gap','restart_gap') AND id<=?)`, maxID).Scan(&gap); err != nil {
			return 0, err
		}
		if gap {
			out.addReason("capture_gap_in_retained_segment")
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id,time_ms,data FROM observations INDEXED BY observation_exchange
	 WHERE kind='print' AND symbol=? AND timeframe='' AND time_ms>=? AND time_ms<? AND id<=?
	 AND (time_ms>? OR (time_ms=? AND id>?)) ORDER BY time_ms,id LIMIT 1024`, symbol, max(from, cursor.Time), to, maxID, cursor.Time, cursor.Time, cursor.ID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		var raw string
		if err := rows.Scan(&cursor.ID, &cursor.Time, &raw); err != nil {
			return count, err
		}
		count++
		var indexed struct{ Normalized *feed.Tick }
		if len(raw) > 64<<10 || json.Unmarshal([]byte(raw), &indexed) != nil || indexed.Normalized == nil || indexed.Normalized.Symbol != symbol || indexed.Normalized.TsMs != cursor.Time {
			out.addReason("invalid_report")
			continue
		}
		if len(out.Prints) >= md.VolumeProfileMaxPrints {
			return count, md.ErrVolumeProfileTooLarge
		}
		indexed.Normalized.Source = nil
		indexed.Normalized.Symbol = symbol
		out.Prints = append(out.Prints, *indexed.Normalized)
	}
	return count, rows.Err()
}
