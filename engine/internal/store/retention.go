package store

// vacuumFreelistThreshold: reclaim disk when free pages exceed ~64 MB.
const vacuumFreelistThreshold = 64 << 20

// VacuumIfNeeded runs VACUUM when the freelist exceeds vacuumFreelistThreshold,
// reporting whether it ran. Call only when no writer transaction can race the
// VACUUM, which needs exclusive DB access.
func (s *Store) VacuumIfNeeded() (bool, error) {
	var freeCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&freeCount); err != nil {
		return false, err
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return false, err
	}
	if freeCount*pageSize <= vacuumFreelistThreshold {
		return false, nil
	}
	if _, err := s.db.Exec("VACUUM"); err != nil {
		return false, err
	}
	return true, nil
}

var (
	vacuumAdviseFreeBytes int64 = 4 << 30
	vacuumBackstopFloor   int64 = 6 << 30
)

func vacuumBackstopThreshold(fileBytes int64) int64 {
	if h := fileBytes / 2; h > vacuumBackstopFloor {
		return h
	}
	return vacuumBackstopFloor
}

// SizeStats is the DB's physical size profile (PRAGMA page_size/page_count/
// freelist_count).
type SizeStats struct{ PageSize, PageCount, FreelistPages int64 }

func (st SizeStats) FileBytes() int64 { return st.PageSize * st.PageCount }
func (st SizeStats) FreeBytes() int64 { return st.PageSize * st.FreelistPages }

func (st SizeStats) NeedsBackstopVacuum() bool {
	return st.FreeBytes() > vacuumBackstopThreshold(st.FileBytes())
}

func (st SizeStats) AdviseVacuum() bool { return st.FreeBytes() > vacuumAdviseFreeBytes }

func (s *Store) SizeStats() (SizeStats, error) {
	var st SizeStats
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&st.PageSize); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&st.PageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&st.FreelistPages); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}
