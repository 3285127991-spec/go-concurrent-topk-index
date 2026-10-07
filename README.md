# topkindex

A small, dependency-free, in-memory **concurrent Top-K index** for Go.
Pure standard library; runs locally on Windows (and anywhere Go runs).

## What it does

- Stores elements of `{id string, score int64, value string}`; ids are unique and non-empty.
- A new id gets a strictly increasing `Seq` on first insert; updating an existing id keeps its `Seq`.
- Ranking: higher `score` first; ties broken by smaller (earlier) `Seq`.
- All elements are kept, partitioned exactly: `winners` holds the current Top-K, `rest` everything else.
- Both partitions are indexed heaps plus an id index, so `Upsert`, `Remove` and the cross-partition
  rebalance after a score update are **O(log n)** — no full rescans or re-sorts on writes.
- Invariants after every mutation: `len(winners) == min(k, Len)`, and the worst winner never ranks
  behind the best of the rest.
- Safe for concurrent use via a single `sync.RWMutex` (writes exclusive, reads shared).

## API

| Call | Description |
| --- | --- |
| `New(k) (*Index, error)` | Create an index; `k > 0` required. |
| `Upsert(id, score, value) error` | Insert or update; rejects empty id; `ErrClosed` after `Close`. |
| `Remove(id) error` | Delete by id (missing id is a no-op); `ErrClosed` after `Close`. |
| `Get(id) (Element, bool)` | Fetch one element copy. |
| `Top() []Element` | Ordered independent copy of the current top `min(k, Len)`. |
| `Snapshot() Snapshot` | All elements in rank order (independent copy) plus `K` and `Closed`. |
| `Len() int` | Total number of elements. |
| `Close()` | Idempotent; rejects writes, keeps reads working. |

## Quick start

```go
ix, _ := topkindex.New(3)
_ = ix.Upsert("alpha", 10, "first")
_ = ix.Upsert("bravo", 30, "second")
for _, e := range ix.Top() {
	fmt.Println(e.ID, e.Score, e.Seq)
}
```

Run the tests (including the race detector) and the demo CLI:

```sh
go test -race ./...
go run ./cmd/topkdemo
```

The demo walks through inserts, stable ordering on equal scores, score updates that cross the
Top-K boundary in both directions, removal with backfill, and reads after `Close`.

## Layout

- `index.go` — public API, partition invariants, rebalance logic.
- `heap.go` — internal entry type and the indexed heap used for both partitions.
- `index_test.go` — eight tests: basic Top-K, tie stability, promote/demote on update,
  remove with backfill, k larger than the set, snapshot/close semantics, basic concurrency.
- `cmd/topkdemo/main.go` — the demo CLI.

## Non-goals

No persistence, no random sampling, no network, no GUI, no database. In-memory only.
