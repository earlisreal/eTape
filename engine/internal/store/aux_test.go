package store

import "testing"

func TestConfigCRUD(t *testing.T) {
	s := open(t)
	s.SetConfig("hotkeys", `{"buy":"b"}`)
	s.SetConfig("theme", `"dark"`)
	s.Flush()

	v, ok, err := s.GetConfig("hotkeys")
	if err != nil || !ok || v != `{"buy":"b"}` {
		t.Fatalf("GetConfig hotkeys = %q ok=%v err=%v", v, ok, err)
	}
	if _, ok, _ := s.GetConfig("missing"); ok {
		t.Fatal("missing key reported present")
	}
	// Overwrite + delete.
	s.SetConfig("theme", `"light"`)
	s.DeleteConfig("hotkeys")
	s.Flush()
	all, err := s.ListConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["theme"] != `"light"` {
		t.Fatalf("ListConfig = %v, want {theme:\"light\"}", all)
	}
}

func TestSysEventsAppendRecent(t *testing.T) {
	s := open(t) // Fake clock frozen at ms 0
	s.AppendSysEvent("boot", "engine up")
	s.AppendSysEvent("gap", "US.AAPL feed gap")
	s.Flush()
	evs, err := s.RecentSysEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("sys_events = %d, want 2", len(evs))
	}
	if evs[0].Kind != "gap" || evs[1].Kind != "boot" { // newest first
		t.Fatalf("order wrong: %+v", evs)
	}
	if evs[0].TsMs != 0 {
		t.Fatalf("ts not from injected clock: %d", evs[0].TsMs)
	}
}
