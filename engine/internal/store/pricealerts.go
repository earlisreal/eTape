package store

import (
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/pricealert"
)

type priceAlertOp struct {
	change pricealert.Change
	done   chan error
}

func (priceAlertOp) render() []pendingWrite { return nil }

func (s *Store) LoadPriceAlerts() ([]pricealert.Alert, []pricealert.Trigger, error) {
	alerts, err := loadPriceAlertRows[pricealert.Alert](s, "SELECT payload FROM price_alerts ORDER BY id")
	if err != nil {
		return nil, nil, err
	}
	history, err := loadPriceAlertRows[pricealert.Trigger](s, "SELECT payload FROM price_alert_triggers ORDER BY ts DESC,id")
	return alerts, history, err
}

func loadPriceAlertRows[T any](s *Store, query string) ([]T, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// CommitPriceAlerts durably saves an alert and its trigger in one writer-owned transaction.
func (s *Store) CommitPriceAlerts(change pricealert.Change) error {
	op := priceAlertOp{change: change, done: make(chan error, 1)}
	s.writes <- op
	return <-op.done
}

func (s *Store) commitPriceAlerts(change pricealert.Change) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if change.DeleteAlert != "" {
		if _, err := tx.Exec("DELETE FROM price_alerts WHERE id=?", change.DeleteAlert); err != nil {
			return err
		}
	}
	if change.DeleteTrigger != "" {
		if _, err := tx.Exec("DELETE FROM price_alert_triggers WHERE id=?", change.DeleteTrigger); err != nil {
			return err
		}
	}
	if change.Alert != nil {
		raw, err := json.Marshal(change.Alert)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO price_alerts(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", change.Alert.ID, string(raw)); err != nil {
			return err
		}
	}
	if change.Trigger != nil {
		raw, err := json.Marshal(change.Trigger)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO price_alert_triggers(id,alert_id,ts,payload) VALUES(?,?,?,?)", change.Trigger.ID, change.Trigger.AlertID, change.Trigger.TsMs, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
