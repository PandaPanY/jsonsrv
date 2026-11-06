package store

// Update 的拷贝、序列化与落盘全程不得阻塞读请求。
// 用测试钩子在落盘前暂停，断言此时 Snapshot 立刻返回旧数据。
// 如果 Update 全程持有 s.mu 写锁，Snapshot 只能等落盘结束，本测试会超时失败。

import (
	"testing"
	"time"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

func TestSnapshotDuringPausedSave(t *testing.T) {
	path := writeDB(t, `{"posts":[1]}`)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	savePauseHook = func() { close(entered); <-release }
	t.Cleanup(func() { savePauseHook = nil })

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- st.Update(func(db *jsonx.Object) error {
			v, perr := jsonx.Parse([]byte(`[1,2]`))
			if perr != nil {
				return perr
			}
			db.Set("posts", v)
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Update 未能进入落盘暂停点")
	}

	// 落盘挂起中：Snapshot 必须立刻返回旧数据（换入发生在落盘之后）。
	got := make(chan int, 1)
	go func() {
		arr, _ := jsonx.AsArray(st.Snapshot().MustGet("posts"))
		got <- len(arr)
	}()
	select {
	case n := <-got:
		if n != 1 {
			t.Fatalf("落盘期间快照应仍是旧数据（1 条），实际 %d 条", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("落盘期间 Snapshot 被写者阻塞")
	}

	close(release)
	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Update 未能返回")
	}
	arr, _ := jsonx.AsArray(st.Snapshot().MustGet("posts"))
	if len(arr) != 2 {
		t.Fatalf("Update 返回后快照应见新数据（2 条），实际 %d 条", len(arr))
	}
}
