package wailsruntime

import "testing"

func TestHintQueueCoalescesBeforeApplyingItsBound(t *testing.T) {
	queue := NewHintQueue(2)
	if !queue.Push(Hint{Key: "workspace", Revision: 1}) {
		t.Fatal("first hint was rejected")
	}
	if !queue.Push(Hint{Key: "other", Revision: 1}) {
		t.Fatal("second hint was rejected")
	}
	if !queue.Push(Hint{Key: "workspace", Revision: 2}) {
		t.Fatal("coalescing an admitted key was rejected")
	}
	if queue.Push(Hint{Key: "third", Revision: 1}) {
		t.Fatal("a new key exceeded the bounded queue")
	}
	if got := queue.Dropped(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}

	hint, ok := queue.Pop()
	if !ok || hint.Key != "workspace" || hint.Revision != 2 {
		t.Fatalf("first pop = %#v, %v; want workspace revision 2", hint, ok)
	}
}

func TestOnlyApplicationHintsMayUseOrdinaryEvents(t *testing.T) {
	for _, class := range []EventClass{
		EventHighFrequency,
		EventTargeted,
		EventPersistenceCritical,
		EventOrderCritical,
	} {
		if EventAllowed(class) {
			t.Fatalf("event class %v must not use ordinary Wails events", class)
		}
	}
	if !EventAllowed(EventApplicationHint) {
		t.Fatal("application hints should use ordinary Wails events")
	}
}

func TestHintQueueKeepsNewestRevision(t *testing.T) {
	queue := NewHintQueue(1)
	queue.Push(Hint{Key: "workspace", Revision: 3})
	queue.Push(Hint{Key: "workspace", Revision: 2})

	hint, ok := queue.Pop()
	if !ok || hint.Revision != 3 {
		t.Fatalf("out-of-order pop = %#v, %v; want revision 3", hint, ok)
	}
}
