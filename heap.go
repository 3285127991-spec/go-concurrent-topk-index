package topkindex

import "container/heap"

// entry is the internal mutable record for one indexed element.
type entry struct {
	id      string
	score   int64
	value   string
	seq     uint64
	winners bool // true while the entry lives in the winners heap
	heapIdx int  // position of the entry within its current heap
}

func (e *entry) element() Element {
	return Element{ID: e.id, Score: e.score, Value: e.value, Seq: e.seq}
}

// better reports whether a ranks strictly ahead of b: higher score first,
// ties broken by lower (earlier assigned) seq.
func better(a, b *entry) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.seq < b.seq
}

// entryHeap is an indexed binary heap of entries ordered by less.
// It implements heap.Interface and keeps entry.heapIdx current.
type entryHeap struct {
	items []*entry
	less  func(a, b *entry) bool
}

func newEntryHeap(less func(a, b *entry) bool) *entryHeap {
	return &entryHeap{less: less}
}

func (h entryHeap) Len() int { return len(h.items) }

func (h entryHeap) Less(i, j int) bool { return h.less(h.items[i], h.items[j]) }

func (h entryHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].heapIdx = i
	h.items[j].heapIdx = j
}

func (h *entryHeap) Push(x any) {
	e := x.(*entry)
	e.heapIdx = len(h.items)
	h.items = append(h.items, e)
}

func (h *entryHeap) Pop() any {
	old := h.items
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	h.items = old[:n-1]
	e.heapIdx = -1
	return e
}

// The helpers below mutate the two partition heaps; the caller must hold
// the index write lock.

func (ix *Index) pushWinners(e *entry) {
	e.winners = true
	heap.Push(ix.winners, e)
}

func (ix *Index) pushRest(e *entry) {
	e.winners = false
	heap.Push(ix.rest, e)
}

func (ix *Index) removeFromHeap(e *entry) {
	if e.winners {
		heap.Remove(ix.winners, e.heapIdx)
	} else {
		heap.Remove(ix.rest, e.heapIdx)
	}
}

func (ix *Index) fix(e *entry) {
	if e.winners {
		heap.Fix(ix.winners, e.heapIdx)
	} else {
		heap.Fix(ix.rest, e.heapIdx)
	}
}
