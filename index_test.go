package topkindex_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"topkindex"
)

func ids(elems []topkindex.Element) []string {
	out := make([]string, len(elems))
	for i, e := range elems {
		out[i] = e.ID
	}
	return out
}

func mustUpsert(t *testing.T, ix *topkindex.Index, id string, score int64, value string) {
	t.Helper()
	if err := ix.Upsert(id, score, value); err != nil {
		t.Fatalf("Upsert(%q): %v", id, err)
	}
}

// requireConsistent checks the black-box invariant: Top equals the first
// min(k, Len) elements of the rank-ordered Snapshot.
func requireConsistent(t *testing.T, ix *topkindex.Index) {
	t.Helper()
	snap := ix.Snapshot()
	top := ix.Top()
	want := min(snap.K, len(snap.Elements))
	if len(top) != want {
		t.Fatalf("Top len = %d, want min(k, Len) = %d", len(top), want)
	}
	if !reflect.DeepEqual(ids(top), ids(snap.Elements[:want])) {
		t.Fatalf("Top %v disagrees with Snapshot prefix %v", ids(top), ids(snap.Elements[:want]))
	}
}

func TestBasicTopK(t *testing.T) {
	if _, err := topkindex.New(0); !errors.Is(err, topkindex.ErrInvalidK) {
		t.Fatalf("New(0) = %v, want ErrInvalidK", err)
	}
	ix, err := topkindex.New(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Upsert("", 1, "x"); !errors.Is(err, topkindex.ErrEmptyID) {
		t.Fatalf("Upsert(empty id) = %v, want ErrEmptyID", err)
	}
	mustUpsert(t, ix, "a", 10, "va")
	mustUpsert(t, ix, "b", 30, "vb")
	mustUpsert(t, ix, "c", 20, "vc")
	mustUpsert(t, ix, "d", 40, "vd")
	mustUpsert(t, ix, "e", 5, "ve")

	if got := ix.Len(); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}
	want := []string{"d", "b", "c"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top = %v, want %v", got, want)
	}
	el, ok := ix.Get("c")
	if !ok || el.Score != 20 || el.Value != "vc" {
		t.Fatalf("Get(c) = %+v, %v", el, ok)
	}
	requireConsistent(t, ix)
}

func TestTieStableBySeq(t *testing.T) {
	ix, err := topkindex.New(3)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "x", 7, "1st")
	mustUpsert(t, ix, "y", 7, "2nd")
	mustUpsert(t, ix, "z", 7, "3rd")
	mustUpsert(t, ix, "w", 1, "low")

	want := []string{"x", "y", "z"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top = %v, want %v (insertion order on ties)", got, want)
	}
	// Updating an existing id must keep its Seq and tie position.
	mustUpsert(t, ix, "y", 7, "2nd-updated")
	el, ok := ix.Get("y")
	if !ok || el.Seq != 1 || el.Value != "2nd-updated" {
		t.Fatalf("Get(y) = %+v, %v; want Seq=1 and updated value", el, ok)
	}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top after update = %v, want %v", got, want)
	}
	requireConsistent(t, ix)
}

func TestUpdatePromote(t *testing.T) {
	ix, err := topkindex.New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "a", 10, "")
	mustUpsert(t, ix, "b", 20, "")
	mustUpsert(t, ix, "c", 30, "")
	if got := ids(ix.Top()); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("Top = %v, want [c b]", got)
	}
	mustUpsert(t, ix, "a", 40, "promoted")
	want := []string{"a", "c"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top after promote = %v, want %v", got, want)
	}
	if el, _ := ix.Get("a"); el.Seq != 0 {
		t.Fatalf("Seq of a = %d after update, want 0", el.Seq)
	}
	requireConsistent(t, ix)
}

func TestUpdateDemote(t *testing.T) {
	ix, err := topkindex.New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "a", 10, "")
	mustUpsert(t, ix, "b", 20, "")
	mustUpsert(t, ix, "c", 30, "")
	mustUpsert(t, ix, "c", 0, "demoted")
	want := []string{"b", "a"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top after demote = %v, want %v", got, want)
	}
	requireConsistent(t, ix)
}

func TestRemoveBackfill(t *testing.T) {
	ix, err := topkindex.New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "a", 10, "")
	mustUpsert(t, ix, "b", 20, "")
	mustUpsert(t, ix, "c", 30, "")
	if err := ix.Remove("c"); err != nil {
		t.Fatal(err)
	}
	want := []string{"b", "a"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top after remove = %v, want %v", got, want)
	}
	if got := ix.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	if err := ix.Remove("missing"); err != nil {
		t.Fatalf("Remove(missing) = %v, want nil", err)
	}
	if _, ok := ix.Get("c"); ok {
		t.Fatal("Get(c) still present after Remove")
	}
	requireConsistent(t, ix)
}

func TestKLargerThanLen(t *testing.T) {
	ix, err := topkindex.New(10)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "a", 1, "")
	mustUpsert(t, ix, "b", 3, "")
	mustUpsert(t, ix, "c", 2, "")
	want := []string{"b", "c", "a"}
	if got := ids(ix.Top()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Top = %v, want %v", got, want)
	}
	snap := ix.Snapshot()
	if snap.K != 10 || len(snap.Elements) != 3 {
		t.Fatalf("Snapshot = K:%d Elements:%d, want K:10 Elements:3", snap.K, len(snap.Elements))
	}
	requireConsistent(t, ix)
}

func TestSnapshotAndClose(t *testing.T) {
	ix, err := topkindex.New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, ix, "a", 10, "va")
	mustUpsert(t, ix, "b", 20, "vb")
	mustUpsert(t, ix, "c", 30, "vc")

	snap := ix.Snapshot()
	if snap.K != 2 || snap.Closed {
		t.Fatalf("Snapshot = K:%d Closed:%v, want K:2 Closed:false", snap.K, snap.Closed)
	}
	if got := ids(snap.Elements); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("Snapshot order = %v, want [c b a]", got)
	}
	// The snapshot is an independent copy.
	snap.Elements[0].Value = "mutated"
	if el, _ := ix.Get("c"); el.Value != "vc" {
		t.Fatalf("index changed after mutating snapshot copy: %+v", el)
	}

	ix.Close()
	ix.Close() // idempotent
	if err := ix.Upsert("d", 99, ""); !errors.Is(err, topkindex.ErrClosed) {
		t.Fatalf("Upsert after Close = %v, want ErrClosed", err)
	}
	if err := ix.Remove("a"); !errors.Is(err, topkindex.ErrClosed) {
		t.Fatalf("Remove after Close = %v, want ErrClosed", err)
	}
	// Reads keep working after Close.
	if got := ids(ix.Top()); !reflect.DeepEqual(got, []string{"c", "b"}) {
		t.Fatalf("Top after Close = %v, want [c b]", got)
	}
	if got := ix.Len(); got != 3 {
		t.Fatalf("Len after Close = %d, want 3", got)
	}
	if snap2 := ix.Snapshot(); !snap2.Closed {
		t.Fatal("Snapshot after Close: Closed = false, want true")
	}
	if el, ok := ix.Get("a"); !ok || el.Score != 10 {
		t.Fatalf("Get(a) after Close = %+v, %v", el, ok)
	}
}

func TestConcurrent(t *testing.T) {
	ix, err := topkindex.New(10)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := fmt.Sprintf("g%d-%02d", g, i)
				if err := ix.Upsert(id, int64(i), "v"); err != nil {
					t.Error(err)
					return
				}
				ix.Top()
				ix.Len()
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			ix.Snapshot()
			ix.Get("g0-00")
		}
	}()
	wg.Wait()

	if got := ix.Len(); got != 100 {
		t.Fatalf("Len = %d, want 100", got)
	}
	top := ix.Top()
	if len(top) != 10 {
		t.Fatalf("Top len = %d, want 10", len(top))
	}
	// Every goroutine wrote scores 0..24, so the top 10 all have score >= 15.
	for _, e := range top {
		if e.Score < 15 {
			t.Fatalf("unexpected element in Top: %+v", e)
		}
	}
	requireConsistent(t, ix)
}
