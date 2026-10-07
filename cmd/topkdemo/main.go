// Command topkdemo is a small CLI that demonstrates the topkindex
// library: inserts, stable ordering on equal scores, score updates that
// cross the Top-K partition in both directions, removal with backfill,
// and reads after Close.
package main

import (
	"errors"
	"fmt"
	"log"

	"topkindex"
)

func printTop(ix *topkindex.Index) {
	for i, e := range ix.Top() {
		fmt.Printf("  %d. %-8s score=%-4d seq=%d value=%q\n", i+1, e.ID, e.Score, e.Seq, e.Value)
	}
	fmt.Printf("  Len=%d\n", ix.Len())
}

func mustUpsert(ix *topkindex.Index, id string, score int64, value string) {
	if err := ix.Upsert(id, score, value); err != nil {
		log.Fatalf("upsert %s: %v", id, err)
	}
}

func main() {
	ix, err := topkindex.New(3)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("== 1. Insert five elements (k=3) ==")
	mustUpsert(ix, "alpha", 10, "first")
	mustUpsert(ix, "bravo", 30, "second")
	mustUpsert(ix, "charlie", 20, "third")
	mustUpsert(ix, "delta", 40, "fourth")
	mustUpsert(ix, "echo", 5, "fifth")
	printTop(ix)

	fmt.Println("== 2. Equal scores keep insertion (Seq) order ==")
	mustUpsert(ix, "foxtrot", 50, "tie 1st")
	mustUpsert(ix, "golf", 50, "tie 2nd")
	mustUpsert(ix, "hotel", 50, "tie 3rd")
	printTop(ix)

	fmt.Println("== 3. Raise echo 5 -> 60: jumps from rest into Top-K ==")
	mustUpsert(ix, "echo", 60, "promoted")
	printTop(ix)

	fmt.Println("== 4. Lower echo 60 -> 1: drops back out of Top-K ==")
	mustUpsert(ix, "echo", 1, "demoted")
	printTop(ix)

	fmt.Println("== 5. Remove foxtrot: best of rest fills the gap ==")
	if err := ix.Remove("foxtrot"); err != nil {
		log.Fatalf("remove: %v", err)
	}
	printTop(ix)

	fmt.Println("== 6. Close: reads keep working, writes are rejected ==")
	ix.Close()
	ix.Close() // idempotent
	printTop(ix)
	if el, ok := ix.Get("golf"); ok {
		fmt.Printf("  Get(golf) -> score=%d value=%q seq=%d\n", el.Score, el.Value, el.Seq)
	}
	snap := ix.Snapshot()
	fmt.Printf("  Snapshot  -> K=%d Closed=%v Elements=%d best=%s\n",
		snap.K, snap.Closed, len(snap.Elements), snap.Elements[0].ID)
	if err := ix.Upsert("india", 99, "rejected"); errors.Is(err, topkindex.ErrClosed) {
		fmt.Println("  Upsert    -> rejected: " + err.Error())
	}
	if err := ix.Remove("golf"); errors.Is(err, topkindex.ErrClosed) {
		fmt.Println("  Remove    -> rejected: " + err.Error())
	}
}
