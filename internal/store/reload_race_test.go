package store

// 丢失更新回归测试：文件监听 Reload 读到保存前的旧内容时，
// 不得把内存数据回滚。Reload 的 stat/读取如果在锁外，换入时可能覆盖
// 并发 Update 已提交的结果。

import (
	"fmt"
	"sync"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

func TestConcurrentUpdateVsReload(t *testing.T) {
	path := writeDB(t, `{"posts":[]}`)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var rwg sync.WaitGroup
	// 疯狂 Reload：放大 stat/读取与保存之间的竞态窗口。
	for i := 0; i < 4; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = st.Reload()
				}
			}
		}()
	}

	var wg sync.WaitGroup
	const n = 200
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := st.Update(func(db *jsonx.Object) error {
				arr, _ := jsonx.AsArray(db.MustGet("posts"))
				v, err := jsonx.Parse([]byte(fmt.Sprintf(`{"id":%d}`, i)))
				if err != nil {
					return err
				}
				db.Set("posts", append(arr, v))
				return nil
			})
			if err != nil {
				t.Errorf("Update: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	arr, _ := jsonx.AsArray(st.Snapshot().MustGet("posts"))
	if len(arr) != n {
		t.Fatalf("丢失更新：内存 posts 数 = %d，期望 %d", len(arr), n)
	}
	// 落盘内容也必须完整（Reload 后内存与文件一致）。
	if _, err := st.Reload(); err != nil {
		t.Fatal(err)
	}
	arr, _ = jsonx.AsArray(st.Snapshot().MustGet("posts"))
	if len(arr) != n {
		t.Fatalf("丢失更新：Reload 后 posts 数 = %d，期望 %d", len(arr), n)
	}
}
