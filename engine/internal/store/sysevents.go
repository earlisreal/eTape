package store

const sysEventInsertSQL = `INSERT INTO sys_events (ts, kind, detail) VALUES (?, ?, ?)`

// SysEvent is one system-log entry.
type SysEvent struct {
	Seq    int64
	TsMs   int64
	Kind   string
	Detail string
}

type sysEventOp struct {
	ts     int64
	kind   string
	detail string
}

func (o sysEventOp) render() []pendingWrite {
	return []pendingWrite{{query: sysEventInsertSQL, args: []any{o.ts, o.kind, o.detail}}}
}

// AppendSysEvent logs a system event stamped with the store clock. Async.
func (s *Store) AppendSysEvent(kind, detail string) {
	s.writes <- sysEventOp{ts: s.clk.Now().UnixMilli(), kind: kind, detail: detail}
}

// RecentSysEvents returns the newest n events, newest first.
func (s *Store) RecentSysEvents(n int) ([]SysEvent, error) {
	rows, err := s.db.Query("SELECT seq, ts, kind, detail FROM sys_events ORDER BY seq DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SysEvent
	for rows.Next() {
		var e SysEvent
		if err := rows.Scan(&e.Seq, &e.TsMs, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
