package watchlist

import (
	"encoding/json"
	"errors"
	"testing"
)

// fakeStore is an in-memory configStore recording Flush calls.
type fakeStore struct {
	kv      map[string]string
	flushes int
}

func newFakeStore() *fakeStore { return &fakeStore{kv: map[string]string{}} }
func (f *fakeStore) GetConfig(key string) (string, bool, error) {
	v, ok := f.kv[key]
	return v, ok, nil
}
func (f *fakeStore) SetConfig(key, value string) { f.kv[key] = value }
func (f *fakeStore) Flush()                      { f.flushes++ }

func TestNewListEmptyWhenAbsent(t *testing.T) {
	l, err := NewList(newFakeStore())
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	if got := l.Symbols(); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestAddNormalizesAndDedupes(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	added, err := l.Add("aapl")
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	if got := l.Symbols(); len(got) != 1 || got[0] != "US.AAPL" {
		t.Fatalf("normalization failed: %v", got)
	}
	added, err = l.Add("US.AAPL") // duplicate
	if err != nil || added {
		t.Fatalf("dup add: added=%v err=%v (want added=false, nil)", added, err)
	}
	if got := l.Symbols(); len(got) != 1 {
		t.Fatalf("dup grew list: %v", got)
	}
}

func TestAddPersistsAndFlushes(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("TSLA")
	if st.flushes == 0 {
		t.Fatal("Add did not Flush")
	}
	raw, ok, _ := st.GetConfig(configKey)
	if !ok {
		t.Fatal("Add did not persist")
	}
	var got []string
	_ = json.Unmarshal([]byte(raw), &got)
	if len(got) != 1 || got[0] != "US.TSLA" {
		t.Fatalf("persisted %v", got)
	}
}

func TestInsertionOrderPreservedAcrossReload(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	for _, s := range []string{"c", "a", "b"} {
		_, _ = l.Add(s)
	}
	l2, _ := NewList(st) // reload from same store
	want := []string{"US.C", "US.A", "US.B"}
	got := l2.Symbols()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order lost: want %v got %v", want, got)
		}
	}
}

func TestRemoveIdempotent(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("AAPL")
	if !l.Remove("US.AAPL") {
		t.Fatal("remove existing should be true")
	}
	if l.Remove("US.AAPL") {
		t.Fatal("remove absent should be false")
	}
	if len(l.Symbols()) != 0 {
		t.Fatal("list not empty after remove")
	}
}

func TestAddRejectsPastCap(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	l.cap = 2 // shrink for test
	_, _ = l.Add("A")
	_, _ = l.Add("B")
	_, err := l.Add("C")
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}

func TestSeedReplacesWholesale(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("OLD")
	l.Seed([]string{"US.VLCN", "US.MERI"})
	got := l.Symbols()
	if len(got) != 2 || got[0] != "US.VLCN" || got[1] != "US.MERI" {
		t.Fatalf("Seed did not replace: %v", got)
	}
}

func TestNewEmptyStartsEmptyAndRemainsFunctional(t *testing.T) {
	// Simulates the main.go fallback: a store whose stored config is
	// unparseable (what would make NewList fail) must still yield a fully
	// usable list via NewEmpty, not a degraded stub.
	st := newFakeStore()
	st.kv[configKey] = "{not valid json"
	if _, err := NewList(st); err == nil {
		t.Fatal("NewList: want error on corrupt config, got nil")
	}

	l := NewEmpty(st)
	if got := l.Symbols(); len(got) != 0 {
		t.Fatalf("NewEmpty: want empty, got %v", got)
	}

	added, err := l.Add("aapl")
	if err != nil || !added {
		t.Fatalf("Add after NewEmpty: added=%v err=%v", added, err)
	}
	if got := l.Symbols(); len(got) != 1 || got[0] != "US.AAPL" {
		t.Fatalf("Add after NewEmpty: normalization/state failed: %v", got)
	}
	raw, ok, _ := st.GetConfig(configKey)
	if !ok {
		t.Fatal("Add after NewEmpty did not persist")
	}
	var persisted []string
	_ = json.Unmarshal([]byte(raw), &persisted)
	if len(persisted) != 1 || persisted[0] != "US.AAPL" {
		t.Fatalf("Add after NewEmpty persisted wrong value: %v (raw=%q)", persisted, raw)
	}
}
