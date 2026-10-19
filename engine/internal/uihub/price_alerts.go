package uihub

import (
	"encoding/json"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/pricealert"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func (h *Hub) SetPriceAlerts(manager *pricealert.Manager) { h.cmd.priceAlerts.Store(manager) }

func mapPriceAlert(a pricealert.Alert) wsmsg.PriceAlert {
	return wsmsg.PriceAlert{ID: a.ID, Symbol: a.Symbol, Condition: string(a.Condition), Price: a.Price, Label: a.Label,
		Sessions: wsmsg.PriceAlertSessions(a.Sessions), State: string(a.State), Version: a.Version, CreatedMs: a.CreatedMs, UpdatedMs: a.UpdatedMs}
}
func mapPriceAlertTriggers(triggers []pricealert.Trigger) []wsmsg.PriceAlertTrigger {
	out := make([]wsmsg.PriceAlertTrigger, 0, len(triggers))
	for _, t := range triggers {
		out = append(out, wsmsg.PriceAlertTrigger{ID: t.ID, AlertID: t.AlertID, Symbol: t.Symbol, Condition: string(t.Condition), Price: t.Price, ActualPrice: t.ActualPrice, Label: t.Label, Sessions: wsmsg.PriceAlertSessions(t.Sessions), Session: t.Session, TsMs: t.TsMs, Reason: t.Reason})
	}
	return out
}
func MapPriceAlertState(s pricealert.Snapshot) wsmsg.PriceAlertState {
	out := wsmsg.PriceAlertState{Alerts: make([]wsmsg.PriceAlert, 0, len(s.Alerts)), HistoryTotal: len(s.History), Revision: s.Revision, Error: s.Error}
	for _, a := range s.Alerts {
		out.Alerts = append(out.Alerts, mapPriceAlert(a))
	}
	history := s.History
	if len(history) > 100 {
		history = history[:100]
	}
	out.History = mapPriceAlertTriggers(history)
	return out
}
func MapPriceAlertNotification(n pricealert.Notification) wsmsg.PriceAlertNotification {
	return wsmsg.PriceAlertNotification{ID: n.ID, Triggers: mapPriceAlertTriggers(n.Triggers), ExpiresMs: n.ExpiresMs}
}

func (cd *commands) handlePriceAlert(name string, args json.RawMessage, connection uint64) (wsmsg.AckMsg, bool) {
	switch name {
	case "UpsertPriceAlert", "DeletePriceAlert", "PausePriceAlert", "DeletePriceAlertTrigger", "ClaimPriceAlertNotification", "QueryPriceAlertHistory", "PreviewPriceAlert":
	default:
		return wsmsg.AckMsg{}, false
	}
	manager := cd.priceAlerts.Load()
	if manager == nil {
		return blocked("price alerts are unavailable"), true
	}
	accepted := func(value any) (wsmsg.AckMsg, bool) {
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted, Value: value}, true
	}
	switch name {
	case "UpsertPriceAlert":
		var a wsmsg.UpsertPriceAlertArgs
		if json.Unmarshal(args, &a) != nil {
			return blocked("bad args"), true
		}
		a.Symbol = strings.ToUpper(strings.TrimSpace(a.Symbol))
		if !strings.HasPrefix(a.Symbol, "US.") {
			a.Symbol = "US." + a.Symbol
		}
		alert, err := manager.Upsert(pricealert.Draft{ID: a.ID, Version: a.Version, Rearm: a.Rearm, Symbol: a.Symbol, Condition: pricealert.Condition(a.Condition), Price: a.Price, Label: a.Label, Sessions: pricealert.Sessions(a.Sessions)})
		if err != nil {
			return blocked(err.Error()), true
		}
		return accepted(mapPriceAlert(alert))
	case "DeletePriceAlert", "PausePriceAlert", "DeletePriceAlertTrigger":
		var a wsmsg.PriceAlertActionArgs
		if json.Unmarshal(args, &a) != nil {
			return blocked("bad args"), true
		}
		var err error
		switch name {
		case "DeletePriceAlert":
			err = manager.Delete(a.ID, a.Version)
		case "PausePriceAlert":
			_, err = manager.SetPaused(a.ID, a.Version, a.Paused)
		default:
			err = manager.DeleteTrigger(a.ID)
		}
		if err != nil {
			return blocked(err.Error()), true
		}
		return accepted(nil)
	case "ClaimPriceAlertNotification":
		var a wsmsg.ClaimPriceAlertNotificationArgs
		if json.Unmarshal(args, &a) != nil {
			return blocked("bad args"), true
		}
		return accepted(manager.ClaimNotification(a.ID, a.Kind, connection))
	case "PreviewPriceAlert":
		var a wsmsg.PriceAlertPreviewArgs
		if json.Unmarshal(args, &a) != nil {
			return blocked("bad args"), true
		}
		p := manager.Preview(strings.ToUpper(strings.TrimSpace(a.Symbol)))
		return accepted(wsmsg.PriceAlertPreview{Price: p.Price, TsMs: p.TsMs})
	case "QueryPriceAlertHistory":
		var a wsmsg.PriceAlertHistoryArgs
		if json.Unmarshal(args, &a) != nil {
			return blocked("bad args"), true
		}
		history := manager.Snapshot().History
		if a.BeforeID != "" {
			found := false
			for i, t := range history {
				if t.ID == a.BeforeID {
					history = history[i+1:]
					found = true
					break
				}
			}
			if !found {
				return blocked("history changed; reload before continuing"), true
			}
		}
		if len(history) > 100 {
			history = history[:100]
		}
		return accepted(mapPriceAlertTriggers(history))
	}
	return accepted(nil)
}
