// Package topkindex provides a concurrent, exact top-K index.
//
// The index stores unique string ids with an int64 score and a string
// value. Every id receives a strictly increasing sequence number on
// first insert; updates keep the original sequence. Ranking orders by
// score descending, ties broken by ascending sequence (earlier inserts
// rank first), which is a total order because sequences are unique.
//
// Internally all elements are kept in two indexed heaps: winners holds
// exactly the current top K (a min-heap by rank, worst winner at the
// root) and rest holds everything else (a max-heap by rank, best
// candidate at the root). Combined with an id index this keeps Upsert,
// Remove and partition rebalancing at O(log n) per operation without
// ever rescanning or re-sorting the full set.
package topkindex

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidK = errors.New("topkindex: k must be > 0")
	ErrEmptyID  = errors.New("topkindex: id must be non-empty")
	ErrNotFound = errors.New("topkindex: id not found")
	ErrClosed   = errors.New("topkindex: index is closed")
)

// Entry is an immutable snapshot of a single stored item.
type Entry struct {
	ID    string
	Score int64
	Value string
	Seq   uint64
}

// Snapshot is a consistent view of the whole index, ranked best first.
type Snapshot struct {
	K      int
	Closed bool
	Items  []Entry
}

type element struct {
	id        string
	score     int64
	value     string
	seq       uint64
	index     int
	inWinners bool
}

// better reports whether a ranks ahead of b: higher score first, ties
// broken by smaller (earlier) sequence.
func better(a, b *element) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.seq < b.seq
}

// elemHeap is an indexed binary heap of elements. The winners heap is a
// min-heap by rank so its root is the worst current winner; the rest
// heap is a max-heap by rank so its root is the best non-winner.
type elemHeap struct {
	winners bool
	elems   []*element
}

func (h *elemHeap) Len() int { return len(h.elems) }

func (h *elemHeap) Less(i, j int) bool {
	if h.winners {
		return better(h.elems[j], h.elems[i])
	}
	return better(h.elems[i], h.elems[j])
}

func (h *elemHeap) Swap(i, j int) {
	h.elems[i], h.elems[j] = h.elems[j], h.elems[i]
	h.elems[i].index = i
	h.elems[j].index = j
}

func (h *elemHeap) Push(x any) {
	e := x.(*element)
	e.index = len(h.elems)
	h.elems = append(h.elems, e)
}

func (h *elemHeap) Pop() any {
	old := h.elems
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	h.elems = old[:n-1]
	return e
}

// Index is a concurrent top-K index. The zero value is not usable;
// construct one with New.
type Index struct {
	mu      sync.RWMutex
	k       int
	nextSeq uint64
	closed  bool
	byID    map[string]*element
	winners elemHeap
	rest    elemHeap
}

// New creates an index keeping the top k elements. k must be > 0.
func New(k int) (*Index, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	x := &Index{
		k:       k,
		nextSeq: 1,
		byID:    make(map[string]*element),
	}
	x.winners.winners = true
	return x, nil
}

// Upsert inserts a new id or updates the score and value of an existing
// one. New ids receive the next strictly increasing sequence; updates
// keep the original sequence. It fails after Close.
func (x *Index) Upsert(id string, score int64, value string) error {
	if id == "" {
		return ErrEmptyID
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return ErrClosed
	}
	if e, ok := x.byID[id]; ok {
		e.score = score
		e.value = value
		x.fix(e)
		x.rebalance()
		return nil
	}
	e := &element{id: id, score: score, value: value, seq: x.nextSeq}
	x.nextSeq++
	x.byID[id] = e
	heap.Push(&x.rest, e)
	x.rebalance()
	return nil
}

// Remove deletes id from the index. It fails after Close and reports
// ErrNotFound for unknown ids.
func (x *Index) Remove(id string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return ErrClosed
	}
	e, ok := x.byID[id]
	if !ok {
		return ErrNotFound
	}
	if e.inWinners {
		heap.Remove(&x.winners, e.index)
	} else {
		heap.Remove(&x.rest, e.index)
	}
	delete(x.byID, id)
	x.rebalance()
	return nil
}

// Get returns a copy of the entry stored under id.
func (x *Index) Get(id string) (Entry, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	e, ok := x.byID[id]
	if !ok {
		return Entry{}, false
	}
	return entryOf(e), true
}

// Top returns an ordered independent copy of the current top K entries,
// best first.
func (x *Index) Top() []Entry {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return sortedEntries(x.winners.elems)
}

// Snapshot returns an ordered independent copy of every entry in the
// index together with K and the closed flag.
func (x *Index) Snapshot() Snapshot {
	x.mu.RLock()
	defer x.mu.RUnlock()
	all := make([]*element, 0, len(x.byID))
	all = append(all, x.winners.elems...)
	all = append(all, x.rest.elems...)
	return Snapshot{K: x.k, Closed: x.closed, Items: sortedEntries(all)}
}

// Len returns the total number of stored entries.
func (x *Index) Len() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.byID)
}

// Close marks the index closed, rejecting further Upsert and Remove
// calls. Reads keep working and Close is idempotent.
func (x *Index) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.closed = true
}

// fix restores the heap invariants after e's score changed in place.
func (x *Index) fix(e *element) {
	if e.inWinners {
		heap.Fix(&x.winners, e.index)
	} else {
		heap.Fix(&x.rest, e.index)
	}
}

// rebalance restores the partition invariant after a mutation: winners
// holds exactly min(k, Len()) elements and no rest element outranks a
// winner. Each step is a single O(log n) heap operation, and a single
// mutation requires only a constant number of them.
func (x *Index) rebalance() {
	for x.winners.Len() < x.k && x.rest.Len() > 0 {
		x.promote(heap.Pop(&x.rest).(*element))
	}
	for x.winners.Len() > 0 && x.rest.Len() > 0 && better(x.rest.elems[0], x.winners.elems[0]) {
		worst := heap.Pop(&x.winners).(*element)
		best := heap.Pop(&x.rest).(*element)
		x.promote(best)
		x.demote(worst)
	}
}

func (x *Index) promote(e *element) {
	e.inWinners = true
	heap.Push(&x.winners, e)
}

func (x *Index) demote(e *element) {
	e.inWinners = false
	heap.Push(&x.rest, e)
}

func entryOf(e *element) Entry {
	return Entry{ID: e.id, Score: e.score, Value: e.value, Seq: e.seq}
}

func sortedEntries(elems []*element) []Entry {
	out := make([]Entry, len(elems))
	for i, e := range elems {
		out[i] = entryOf(e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}
