# go-concurrent-topk-index

并发安全的精确 Top-K 索引。纯 Go 标准库实现，无第三方依赖，包含库、演示 CLI 和自动化测试。

## 功能

- 存储唯一非空 `string` id、`int64` score、`string` value
- 新 id 首次插入时分配严格递增的 `Seq`；更新 score/value 不改变 `Seq`
- 排名规则：score 降序；同分时 `Seq` 小者靠前（`Seq` 唯一，因此是全序）
- 并发安全：读写锁保护，多 goroutine 可同时操作
- `Close` 后拒绝写入（`Upsert`/`Remove` 返回 `ErrClosed`），读取接口仍可用，`Close` 可重复调用

## 设计

索引维护全部元素，并精确维护前 K 名分区：

- `winners`：当前 Top-K，按排名的**小顶堆**，堆顶是最差赢家
- `rest`：其余元素，按排名的**大顶堆**，堆顶是最强备选
- `byID`：id 到元素的索引，元素记录其在堆中的位置

任意时刻不变量：`len(winners) == min(k, Len)`，且 winners 中最差元素不排在 rest 最好元素之后。
每次变更后只需常数次 O(log n) 堆操作即可恢复不变量，无需扫描或重排全部元素。

| 操作 | 复杂度 |
| --- | --- |
| `Upsert` / `Remove` | O(log n) |
| `Get` / `Len` | O(1) |
| `Top` | O(k log k)（对 k 个赢家排序复制） |
| `Snapshot` | O(n log n)（对全部元素排序复制） |

## API

```go
idx, err := topkindex.New(3)                 // k > 0，否则 ErrInvalidK
err = idx.Upsert("alice", 90, "engineer")    // 插入或更新
err = idx.Remove("alice")                    // 未知 id 返回 ErrNotFound
entry, ok := idx.Get("alice")                // 单条读取
top := idx.Top()                             // 前 k 名有序独立副本
snap := idx.Snapshot()                       // 全部元素有序副本 + K + Closed
n := idx.Len()                               // 元素总数
idx.Close()                                  // 关闭写入，可重复调用
```

错误：`ErrInvalidK`、`ErrEmptyID`、`ErrNotFound`、`ErrClosed`，均可用 `errors.Is` 判断。

## 运行

```sh
go test -race ./...     # 运行全部测试（含竞态检测）
go run ./cmd/topk-demo  # 演示：插入、同分稳定、升降分跨分区、Remove 补位、Close 后读取
```

## 测试

共 9 个测试：基础 Top-K、同分按 Seq 稳定、更新升入/降出分区、Remove 补位、k 大于元素数、
Snapshot/Close 语义、并发读写（`-race` 验证）、参数校验。每个变更类用例都会校验分区不变量与堆索引一致性。
