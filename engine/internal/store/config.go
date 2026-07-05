package store

import (
	"database/sql"
	"errors"
)

const (
	configUpsertSQL = `INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)`
	configDeleteSQL = `DELETE FROM config WHERE key=?`
)

type configOp struct {
	query string
	args  []any
}

func (o configOp) render() []pendingWrite { return []pendingWrite{{query: o.query, args: o.args}} }

// SetConfig upserts a JSON config document. Async — call Flush for durability.
func (s *Store) SetConfig(key, value string) {
	s.writes <- configOp{query: configUpsertSQL, args: []any{key, value}}
}

// DeleteConfig removes a config key. Async.
func (s *Store) DeleteConfig(key string) {
	s.writes <- configOp{query: configDeleteSQL, args: []any{key}}
}

// GetConfig reads one key; ok is false when absent.
func (s *Store) GetConfig(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM config WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// ListConfig returns all config documents.
func (s *Store) ListConfig() (map[string]string, error) {
	rows, err := s.db.Query("SELECT key, value FROM config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
