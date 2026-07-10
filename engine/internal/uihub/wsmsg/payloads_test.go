package wsmsg

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthSnapshotQuotaMarshal(t *testing.T) {
	// Absent quota must not appear in JSON (optional field).
	b, _ := json.Marshal(HealthSnapshot{Links: []HealthLink{}})
	if strings.Contains(string(b), "quota") {
		t.Fatalf("quota must be omitted when nil: %s", b)
	}
	// Present quota round-trips with lower-camel keys.
	q := QuotaInfo{SubUsed: 62, SubRemain: 38, SubOwn: 47, SubForeign: 15,
		HistUsed: 41, HistRemain: 59, State: "foreign", HistState: "ok"}
	b, _ = json.Marshal(HealthSnapshot{Links: []HealthLink{}, Quota: &q})
	for _, want := range []string{`"subForeign":15`, `"state":"foreign"`, `"histState":"ok"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
	// SysEvent.Level omitted when empty (existing events unaffected).
	b, _ = json.Marshal(SysEvent{Seq: 1, Kind: "boot", Detail: "up"})
	if strings.Contains(string(b), "level") {
		t.Fatalf("level must be omitted when empty: %s", b)
	}
}
