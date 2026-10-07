// Package topkindex provides a concurrent, exact Top-K index.
//
// The index stores every element while maintaining a precise partition:
// the current Top-K lives in the winners heap and everything else in the
// rest heap. Both heaps are indexed, so Upsert, Remove and the partition
// rebalance after an update each cost O(log n) without rescanning or
// re-sorting the full set.
package topkindex

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

var (
	// ErrClosed is returned by mutating operations after Close.
	ErrClosed = errors.New("topkindex: index is closed")
	// ErrEmptyID is returned when an empty id is supplied.
	ErrEmptyID = errors.New("topkindex: id must not be empty")
	// ErrInvalidK is returned by New when k <= 0.
	ErrInvalidK = errors.New("topkindex: k must be greater than zero")
)

// Element is an immutable copy of one indexed item.
type Element struct {
	ID    string
	Score int64
	Value string
	Seq   uint64
}

// Snapshot is an independent, rank-ordered copy of the whole index.
type Snapshot struct {
	K        int
	Closed   bool
	Elements []Element // every element, best first
}

// Index is a concurrent Top-K index. The zero value is not usable; call New.
type Index struct {
	mu      sync.RWMutex
	k       int
	entries map[string]*entry
	winners *entryHeap // min-heap by rank: the worst winner sits at the root
	rest    *entryHeap // max-heap by rank: the best of the rest sits at the root
	nextSeq uint64
	closed  bool
}

// New creates an index that keeps the top k elements. k must be > 0.
func New(k int) (*Index, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	return &Index{
		k:       k,
		entries: make(map[string]*entry),
		winners: newEntryHeap(func(a, b *entry) bool { return better(b, a) }),
		rest:    newEntryHeap(func(a, b *entry) bool { return better(a, b) }),
	}, nil
}

// Upsert inserts a new element or updates the score and value of an
// existing one. A new id is assigned a strictly increasing Seq; updating
// an existing id keeps its original Seq.
func (ix *Index) Upsert(id string, score int64, value string) error {
	if id == "" {
		return ErrEmptyID
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.closed {
		return ErrClosed
	}
	if e, ok := ix.entries[id]; ok {
		e.score = score
		e.value = value
		ix.fix(e)
	} else {
		e := &entry{id: id, score: score, value: value, seq: ix.nextSeq}
		ix.nextSeq++
		ix.entries[id] = e
		ix.pushRest(e)
	}
	ix.rebalance()
	return nil
}

// Remove deletes id from the index. Removing a missing id is a no-op.
func (ix *Index) Remove(id string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.closed {
		return ErrClosed
	}
	e, ok := ix.entries[id]
	if !ok {
		return nil
	}
	delete(ix.entries, id)
	ix.removeFromHeap(e)
	ix.rebalance()
	return nil
}

// Get returns a copy of the element stored under id.
func (ix *Index) Get(id string) (Element, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	e, ok := ix.entries[id]
	if !ok {
		return Element{}, false
	}
	return e.element(), true
}

// Top returns an ordered, independent copy of the current top
// min(k, Len) elements, best first.
func (ix *Index) Top() []Element {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return rankedElements(ix.winners.items)
}

// Snapshot returns an ordered, independent copy of every element together
// with K and the Closed flag.
func (ix *Index) Snapshot() Snapshot {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	all := make([]*entry, 0, len(ix.entries))
	for _, e := range ix.entries {
		all = append(all, e)
	}
	return Snapshot{K: ix.k, Closed: ix.closed, Elements: rankedElements(all)}
}

// Len returns the total number of elements in the index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries)
}

// Close marks the index closed. Upsert and Remove are rejected afterwards,
// while Get, Top, Snapshot and Len keep working. Close is idempotent.
func (ix *Index) Close() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.closed = true
}

// rebalance restores the partition invariants after a mutation:
//   - len(winners) == min(k, Len)
//   - every winner ranks ahead of every element in rest
//
// The caller must hold the write lock.
func (ix *Index) rebalance() {
	for ix.winners.Len() < ix.k && ix.rest.Len() > 0 {
		ix.pushWinners(heap.Pop(ix.rest).(*entry))
	}
	for ix.winners.Len() > ix.k {
		ix.pushRest(heap.Pop(ix.winners).(*entry))
	}
	for ix.winners.Len() > 0 && ix.rest.Len() > 0 &&
		better(ix.rest.items[0], ix.winners.items[0]) {
		worst := heap.Pop(ix.winners).(*entry)
		best := heap.Pop(ix.rest).(*entry)
		ix.pushWinners(best)
		ix.pushRest(worst)
	}
}

// rankedElements sorts entries by rank and returns them as independent
// Element copies, best first. The input slice is not modified.
func rankedElements(entries []*entry) []Element {
	ranked := make([]*entry, len(entries))
	copy(ranked, entries)
	sort.Slice(ranked, func(i, j int) bool { return better(ranked[i], ranked[j]) })
	out := make([]Element, len(ranked))
	for i, e := range ranked {
		out[i] = e.element()
	}
	return out
}
