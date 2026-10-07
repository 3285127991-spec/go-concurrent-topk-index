package topkindex

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func mustNew(t *testing.T, k int) *Index {
	t.Helper()
	x, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	return x
}

func mustUpsert(t *testing.T, x *Index, id string, score int64, value string) {
	t.Helper()
	if err := x.Upsert(id, score, value); err != nil {
		t.Fatalf("Upsert(%q, %d): %v", id, score, err)
	}
}

func idsOf(entries []Entry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

// checkInvariant verifies the partition invariant and the heap
// bookkeeping: winners holds exactly min(k, Len) elements, every winner
// ranks ahead of every rest element, and heap positions are consistent.
func checkInvariant(t *testing.T, x *Index) {
	t.Helper()
	if total := x.winners.Len() + x.rest.Len(); total != len(x.byID) {
		t.Fatalf("heaps hold %d elements, byID holds %d", total, len(x.byID))
	}
	want := x.k
	if len(x.byID) < want {
		want = len(x.byID)
	}
	if x.winners.Len() != want {
		t.Fatalf("winners=%d, want min(k, Len)=%d", x.winners.Len(), want)
	}
	for i, e := range x.winners.elems {
		if e.index != i || !e.inWinners {
			t.Fatalf("winner %q bookkeeping broken: index=%d inWinners=%v", e.id, e.index, e.inWinners)
		}
	}
	for i, e := range x.rest.elems {
		if e.index != i || e.inWinners {
			t.Fatalf("rest %q bookkeeping broken: index=%d inWinners=%v", e.id, e.index, e.inWinners)
		}
	}
	for _, w := range x.winners.elems {
		for _, r := range x.rest.elems {
			if better(r, w) {
				t.Fatalf("rest element %q outranks winner %q", r.id, w.id)
			}
		}
	}
}

func TestBasicTopK(t *testing.T) {
	x := mustNew(t, 3)
	mustUpsert(t, x, "a", 10, "va")
	mustUpsert(t, x, "b", 50, "vb")
	mustUpsert(t, x, "c", 30, "vc")
	mustUpsert(t, x, "d", 40, "vd")
	mustUpsert(t, x, "e", 20, "ve")

	if got := x.Len(); got != 5 {
		t.Fatalf("Len=%d, want 5", got)
	}
	top := x.Top()
	if want := []string{"b", "d", "c"}; !slices.Equal(idsOf(top), want) {
		t.Fatalf("Top ids=%v, want %v", idsOf(top), want)
	}
	e, ok := x.Get("d")
	if !ok || e.Score != 40 || e.Value != "vd" || e.Seq != 4 {
		t.Fatalf("Get(d)=%+v ok=%v", e, ok)
	}
	if _, ok := x.Get("zzz"); ok {
		t.Fatal("Get(zzz) should miss")
	}
	// The returned copy is independent of later mutations.
	mustUpsert(t, x, "b", 5, "vb2")
	if top[0].Score != 50 || top[0].Value != "vb" {
		t.Fatalf("Top copy changed after mutation: %+v", top[0])
	}
	checkInvariant(t, x)
}

func TestTieStableBySeq(t *testing.T) {
	x := mustNew(t, 3)
	mustUpsert(t, x, "first", 7, "")
	mustUpsert(t, x, "second", 7, "")
	mustUpsert(t, x, "third", 7, "")
	mustUpsert(t, x, "fourth", 7, "")

	if want := []string{"first", "second", "third"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top ids=%v, want %v", idsOf(x.Top()), want)
	}
	// Updating to the same score keeps the original seq and order.
	mustUpsert(t, x, "fourth", 7, "updated")
	if want := []string{"first", "second", "third"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top after tie update=%v, want %v", idsOf(x.Top()), want)
	}
	if e, _ := x.Get("fourth"); e.Seq != 4 {
		t.Fatalf("seq changed on update: %+v", e)
	}
	snap := x.Snapshot()
	if want := []string{"first", "second", "third", "fourth"}; !slices.Equal(idsOf(snap.Items), want) {
		t.Fatalf("Snapshot ids=%v, want %v", idsOf(snap.Items), want)
	}
	checkInvariant(t, x)
}

func TestUpdatePromotesAcrossPartition(t *testing.T) {
	x := mustNew(t, 2)
	mustUpsert(t, x, "a", 10, "")
	mustUpsert(t, x, "b", 20, "")
	mustUpsert(t, x, "c", 30, "")

	before, _ := x.Get("a")
	mustUpsert(t, x, "a", 100, "promoted")

	if want := []string{"a", "c"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top=%v, want %v", idsOf(x.Top()), want)
	}
	after, _ := x.Get("a")
	if after.Seq != before.Seq || after.Value != "promoted" {
		t.Fatalf("update changed seq or value: before=%+v after=%+v", before, after)
	}
	checkInvariant(t, x)
}

func TestUpdateDemotesAcrossPartition(t *testing.T) {
	x := mustNew(t, 2)
	mustUpsert(t, x, "a", 50, "")
	mustUpsert(t, x, "b", 40, "")
	mustUpsert(t, x, "c", 30, "")

	mustUpsert(t, x, "a", 1, "dropped")

	if want := []string{"b", "c"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top=%v, want %v", idsOf(x.Top()), want)
	}
	if e, _ := x.Get("a"); e.Score != 1 || e.Value != "dropped" {
		t.Fatalf("Get(a)=%+v", e)
	}
	checkInvariant(t, x)
}

func TestRemoveBackfills(t *testing.T) {
	x := mustNew(t, 2)
	mustUpsert(t, x, "a", 50, "")
	mustUpsert(t, x, "b", 40, "")
	mustUpsert(t, x, "c", 30, "")

	if err := x.Remove("a"); err != nil {
		t.Fatalf("Remove(a): %v", err)
	}
	if want := []string{"b", "c"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top after removing winner=%v, want %v", idsOf(x.Top()), want)
	}

	mustUpsert(t, x, "d", 60, "")
	if err := x.Remove("b"); err != nil { // remove a rest element
		t.Fatalf("Remove(b): %v", err)
	}
	if want := []string{"d", "c"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top after removing rest element=%v, want %v", idsOf(x.Top()), want)
	}

	if err := x.Remove("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove(missing)=%v, want ErrNotFound", err)
	}
	if got := x.Len(); got != 2 {
		t.Fatalf("Len=%d, want 2", got)
	}
	checkInvariant(t, x)
}

func TestKLargerThanLen(t *testing.T) {
	x := mustNew(t, 10)
	mustUpsert(t, x, "a", 1, "")
	mustUpsert(t, x, "b", 3, "")
	mustUpsert(t, x, "c", 2, "")

	top := x.Top()
	if want := []string{"b", "c", "a"}; !slices.Equal(idsOf(top), want) {
		t.Fatalf("Top=%v, want %v", idsOf(top), want)
	}
	if snap := x.Snapshot(); len(snap.Items) != 3 {
		t.Fatalf("len(Snapshot.Items)=%d, want 3", len(snap.Items))
	}
	checkInvariant(t, x)
}

func TestSnapshotAndClose(t *testing.T) {
	x := mustNew(t, 2)
	mustUpsert(t, x, "a", 1, "va")
	mustUpsert(t, x, "b", 2, "vb")
	mustUpsert(t, x, "c", 3, "vc")

	snap := x.Snapshot()
	if snap.K != 2 || snap.Closed {
		t.Fatalf("Snapshot meta: %+v", snap)
	}
	if want := []string{"c", "b", "a"}; !slices.Equal(idsOf(snap.Items), want) {
		t.Fatalf("Snapshot ids=%v, want %v", idsOf(snap.Items), want)
	}

	x.Close()
	x.Close() // idempotent

	if err := x.Upsert("d", 9, "vd"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Upsert after Close=%v, want ErrClosed", err)
	}
	if err := x.Remove("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Remove after Close=%v, want ErrClosed", err)
	}

	// Reads keep working after Close.
	if got := x.Len(); got != 3 {
		t.Fatalf("Len=%d, want 3", got)
	}
	if e, ok := x.Get("a"); !ok || e.Value != "va" {
		t.Fatalf("Get(a)=%+v ok=%v", e, ok)
	}
	if want := []string{"c", "b"}; !slices.Equal(idsOf(x.Top()), want) {
		t.Fatalf("Top=%v, want %v", idsOf(x.Top()), want)
	}
	snap2 := x.Snapshot()
	if !snap2.Closed || len(snap2.Items) != 3 {
		t.Fatalf("Snapshot after Close: %+v", snap2)
	}

	// Mutating the returned snapshot must not affect the index.
	snap2.Items[0].Score = -999
	if e, _ := x.Get("c"); e.Score != 3 {
		t.Fatalf("index changed through snapshot copy: %+v", e)
	}
	checkInvariant(t, x)
}

func TestConcurrentReadWrite(t *testing.T) {
	x := mustNew(t, 8)
	const (
		goroutines = 8
		ops        = 250
		idSpace    = 32
	)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				id := fmt.Sprintf("id-%d", (g*ops+i)%idSpace)
				switch (g + i) % 6 {
				case 0, 1, 2:
					_ = x.Upsert(id, int64((g*31+i*17)%100), "v")
				case 3:
					_ = x.Remove(id)
				case 4:
					_, _ = x.Get(id)
					_ = x.Len()
				case 5:
					_ = x.Top()
					_ = x.Snapshot()
				}
			}
		}(g)
	}
	wg.Wait()

	checkInvariant(t, x)
	top := x.Top()
	snap := x.Snapshot()
	if len(top) > x.k {
		t.Fatalf("len(Top)=%d > k=%d", len(top), x.k)
	}
	for i := 1; i < len(snap.Items); i++ {
		prev, cur := snap.Items[i-1], snap.Items[i]
		if prev.Score < cur.Score || (prev.Score == cur.Score && prev.Seq > cur.Seq) {
			t.Fatalf("snapshot out of order at %d: %+v then %+v", i, prev, cur)
		}
	}
	for i, e := range top {
		if e != snap.Items[i] {
			t.Fatalf("Top[%d]=%+v != Snapshot.Items[%d]=%+v", i, e, i, snap.Items[i])
		}
	}
}

func TestValidation(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(0)=%v, want ErrInvalidK", err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(-3)=%v, want ErrInvalidK", err)
	}
	x := mustNew(t, 1)
	if err := x.Upsert("", 1, "v"); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Upsert(empty id)=%v, want ErrEmptyID", err)
	}
	if got := x.Len(); got != 0 {
		t.Fatalf("Len=%d, want 0", got)
	}
	if top := x.Top(); len(top) != 0 {
		t.Fatalf("Top on empty index=%v, want empty", top)
	}
}
