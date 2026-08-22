package wailsruntime

import "sync"

type EventClass uint8

const (
	EventApplicationHint EventClass = iota
	EventHighFrequency
	EventTargeted
	EventPersistenceCritical
	EventOrderCritical
)

func EventAllowed(class EventClass) bool { return class == EventApplicationHint }

type Hint struct {
	Key      string
	Revision uint64
	Data     any
}

type HintQueue struct {
	mu      sync.Mutex
	limit   int
	items   []Hint
	indices map[string]int
	dropped uint64
}

func NewHintQueue(limit int) *HintQueue {
	if limit < 1 {
		limit = 1
	}
	return &HintQueue{limit: limit, indices: make(map[string]int)}
}

func (q *HintQueue) Push(hint Hint) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if index, ok := q.indices[hint.Key]; ok {
		q.items[index] = hint
		return true
	}
	if len(q.items) >= q.limit {
		q.dropped++
		return false
	}
	q.indices[hint.Key] = len(q.items)
	q.items = append(q.items, hint)
	return true
}

func (q *HintQueue) Pop() (Hint, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return Hint{}, false
	}

	hint := q.items[0]
	delete(q.indices, hint.Key)
	q.items = q.items[1:]
	for index := range q.items {
		q.indices[q.items[index].Key] = index
	}
	return hint, true
}

func (q *HintQueue) Dropped() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}
