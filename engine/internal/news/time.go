package news

import (
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type parsedPublishTime struct {
	At        string
	Precision string
	OK        bool
}

func parsePublishTime(raw string, now time.Time) parsedPublishTime {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return parsedPublishTime{Precision: "unknown"}
	}
	loc := session.Loc()
	for _, layout := range []string{"2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsedPublishTime{At: iso(t), Precision: "second", OK: true}
		}
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsedPublishTime{At: iso(t), Precision: "date", OK: true}
		}
	}
	for _, layout := range []string{"1/2 15:04", "01/02 15:04", "1/2", "01/02"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			etNow := now.In(loc)
			date := time.Date(etNow.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if date.After(etNow.Add(24 * time.Hour)) {
				date = date.AddDate(-1, 0, 0)
			}
			precision := "second"
			if !strings.Contains(raw, " ") {
				date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
				precision = "date"
			}
			return parsedPublishTime{At: iso(date), Precision: precision, OK: true}
		}
	}
	return parsedPublishTime{Precision: "unknown"}
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

func parseISO(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}
