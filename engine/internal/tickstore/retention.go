package tickstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

var segmentName = regexp.MustCompile(`^tick-[0-9]{8}-[0-9a-f]{32}-[0-9]{6}\.sqlite$`)

func segmentSize(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, errors.New("tickstore: segment or sidecar is not a regular file")
		}
		total += info.Size()
	}
	return total, nil
}

func (s *Store) physicalBytes() (int64, error) {
	entries, err := os.ReadDir(s.opt.Directory)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total, nil
}

// recoverSegments validates ownership and resolves committed WAL before any
// file can become a retention candidate. Unknown files are never removed.
func (s *Store) recoverSegments() error {
	entries, err := os.ReadDir(s.opt.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !segmentName.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(s.opt.Directory, entry.Name())
		if _, err := segmentSize(path); err != nil {
			return err
		}
		if err := s.checkpointBudget(path); err != nil {
			return err
		}
		db, err := openDB(path)
		if err != nil {
			return err
		}
		var owner, run string
		var clean, version int
		var created int64
		err = db.QueryRow("SELECT owner,run_id,clean,created_ms,schema_version FROM tickstore_meta").Scan(&owner, &run, &clean, &created, &version)
		if err != nil || version != 1 || owner != "etape.tickstore" || !strings.Contains(entry.Name(), "-"+run+"-") {
			_ = db.Close()
			continue
		}
		s.segments[path] = created
		var boundary profileBoundary
		if err = db.QueryRow("SELECT COALESCE(MAX(id),0) FROM observations").Scan(&boundary.ID); err == nil {
			err = db.QueryRow("SELECT COALESCE(last_commit_ms,0) FROM tickstore_meta").Scan(&boundary.AsOfMs)
		}
		if err != nil {
			_ = db.Close()
			return err
		}
		s.profileBounds[path] = boundary
		if err = checkpoint(db); err == nil && clean == 0 {
			s.incomplete = true
			if !slices.Contains(s.recovered, run) {
				s.recovered = append(s.recovered, run)
			}
			_, err = db.Exec("UPDATE tickstore_meta SET attribution_incomplete=1")
			if err == nil {
				err = checkpoint(db)
			}
		}
		closeErr := db.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *Store) ensureBudget(growth int64) error {
	if growth < 0 || growth > s.opt.MaxBytes {
		return errors.New("tickstore: operation exceeds physical archive budget")
	}
	free, err := s.opt.FreeSpace(s.opt.Directory)
	if err != nil {
		return fmt.Errorf("tickstore: free disk lookup: %w", err)
	}
	total, err := s.physicalBytes()
	if err != nil {
		return err
	}
	var candidates []string
	for path := range s.segments {
		if path != s.path {
			candidates = append(candidates, path)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := s.segments[candidates[i]], s.segments[candidates[j]]
		if a != b {
			return a < b
		}
		return candidates[i] < candidates[j]
	})
	now := s.opt.Now().In(session.Loc())
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, session.Loc()).AddDate(0, 0, 1-s.opt.RetentionDays).UnixMilli()
	for _, path := range candidates {
		need := total+growth > s.opt.MaxBytes || free < uint64(growth)+s.opt.MinFreeBytes
		if !need && s.segments[path] >= cutoff {
			continue
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, err := segmentSize(path); err != nil {
			continue
		}
		if err := s.checkpointBudget(path); err != nil {
			continue
		}
		db, err := openDB(path)
		if err != nil {
			continue
		}
		var owner, run string
		var created int64
		var version int
		err = db.QueryRow("SELECT owner,run_id,created_ms,schema_version FROM tickstore_meta").Scan(&owner, &run, &created, &version)
		if err != nil || version != 1 || owner != "etape.tickstore" || !strings.Contains(filepath.Base(path), "-"+run+"-") {
			_ = db.Close()
			continue
		}
		if !need && created >= cutoff {
			_ = db.Close()
			continue
		}
		// Includes an unclean predecessor: SQLite recovery/checkpoint must
		// succeed before whole-file removal is safe.
		err = checkpoint(db)
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			continue
		}
		// Names came from this resolved directory; reject alternate paths
		// and links, including a concurrently replaced entry.
		if filepath.Dir(path) != s.opt.Directory || !segmentName.MatchString(filepath.Base(path)) {
			continue
		}
		info, err = os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		failed := false
		for _, suffix := range []string{"-wal", "-shm", ""} {
			target := path + suffix
			if !strings.HasPrefix(target, s.opt.Directory+string(filepath.Separator)) {
				failed = true
				break
			}
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		delete(s.segments, path)
		delete(s.profileBounds, path)
		s.incomplete = true // surviving references may name deleted original payloads
		total, err = s.physicalBytes()
		if err != nil {
			return err
		}
		free, err = s.opt.FreeSpace(s.opt.Directory)
		if err != nil {
			return err
		}
	}
	if total+growth > s.opt.MaxBytes {
		return errors.New("tickstore: physical archive cap reached")
	}
	if free < s.opt.MinFreeBytes || free-s.opt.MinFreeBytes < uint64(growth) {
		return errors.New("tickstore: free disk reserve reached")
	}
	return nil
}

func (s *Store) checkpointBudget(path string) error {
	// Recovery can copy WAL pages into the database before truncating WAL.
	var reserve int64 = 128 << 10
	if info, statErr := os.Stat(path + "-wal"); statErr == nil {
		reserve += info.Size()
	}
	total, budgetErr := s.physicalBytes()
	free, freeErr := s.opt.FreeSpace(s.opt.Directory)
	if budgetErr != nil || freeErr != nil || total+reserve > s.opt.MaxBytes || free < s.opt.MinFreeBytes+uint64(reserve) {
		return errors.Join(errors.New("tickstore: recovery reserve unavailable; previous evidence retained"), budgetErr, freeErr)
	}
	return nil
}
