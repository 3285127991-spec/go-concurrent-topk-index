// Command topk-demo demonstrates the topkindex library: inserts,
// tie-stable ordering, score updates crossing the winners/rest
// partition, removal with backfill, and reads after Close.
package main

import (
	"errors"
	"fmt"

	"topkindex"
)

func printTop(idx *topkindex.Index) {
	for i, e := range idx.Top() {
		fmt.Printf("  %d. %-6s score=%-4d seq=%-2d value=%q\n", i+1, e.ID, e.Score, e.Seq, e.Value)
	}
	fmt.Printf("  (len=%d)\n\n", idx.Len())
}

func main() {
	idx, err := topkindex.New(3)
	if err != nil {
		panic(err)
	}

	fmt.Println("== 1. insert five entries, k=3 ==")
	idx.Upsert("alice", 90, "engineer")
	idx.Upsert("bob", 80, "designer")
	idx.Upsert("carol", 70, "manager")
	idx.Upsert("dave", 60, "analyst")
	idx.Upsert("erin", 50, "intern")
	printTop(idx)

	fmt.Println("== 2. ties keep insertion order (frank, gina also score 80) ==")
	idx.Upsert("frank", 80, "writer")
	idx.Upsert("gina", 80, "editor")
	printTop(idx)

	fmt.Println("== 3. erin promoted across the partition: 50 -> 95 ==")
	idx.Upsert("erin", 95, "intern")
	printTop(idx)

	fmt.Println("== 4. alice demoted across the partition: 90 -> 10 ==")
	idx.Upsert("alice", 10, "engineer")
	printTop(idx)

	fmt.Println("== 5. remove erin, best of rest backfills ==")
	if err := idx.Remove("erin"); err != nil {
		panic(err)
	}
	printTop(idx)

	fmt.Println("== 6. close: writes rejected, reads still work ==")
	idx.Close()
	idx.Close() // idempotent
	if err := idx.Upsert("heidi", 99, "late"); errors.Is(err, topkindex.ErrClosed) {
		fmt.Println("  Upsert rejected:", err)
	}
	if err := idx.Remove("bob"); errors.Is(err, topkindex.ErrClosed) {
		fmt.Println("  Remove rejected:", err)
	}
	if e, ok := idx.Get("bob"); ok {
		fmt.Printf("  Get(bob) still works: score=%d value=%q\n", e.Score, e.Value)
	}
	snap := idx.Snapshot()
	fmt.Printf("  Snapshot: k=%d closed=%v items=%d\n", snap.K, snap.Closed, len(snap.Items))
	printTop(idx)
}
