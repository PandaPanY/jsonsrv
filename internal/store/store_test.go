package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// writeDB 在临时目录写入数据文件并返回路径。
func writeDB(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open("不存在的文件.json"); err == nil {
		t.Fatal("不存在文件应报错")
	}
	if _, err := Open(writeDB(t, "not json")); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if _, err := Open(writeDB(t, `[1,2]`)); err == nil {
		t.Fatal("顶层数组应报错")
	}
	if _, err := Open(writeDB(t, `{"a":`)); err == nil {
		t.Fatal("截断 JSON 应报错")
	}
}

func TestOpenJSON5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json5")
	if err := os.WriteFile(path, []byte("{ a: 1, }"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("json5 应可加载: %v", err)
	}
	if st.Snapshot().MustGet("a") != json.Number("1") {
		t.Fatal("内容不符")
	}
}

func TestUpdatePersistsFormatted(t *testing.T) {
	initial := "{\n  \"b\": 1,\n  \"a\": {\n    \"x\": []\n  }\n}\n"
	path := writeDB(t, initial)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = st.Update(func(db *jsonx.Object) error {
		db.Set("b", json.Number("2"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "{\n  \"b\": 2,\n  \"a\": {\n    \"x\": []\n  }\n}\n"
	if string(data) != want {
		t.Fatalf("保存结果应保持 2 空格缩进与键顺序:\n得到 %q\n期望 %q", data, want)
	}
}

func TestUpdateErrorKeepsState(t *testing.T) {
	path := writeDB(t, `{"a": 1}`)
	st, _ := Open(path)
	before, _ := os.ReadFile(path)
	err := st.Update(func(db *jsonx.Object) error {
		db.Set("a", json.Number("99"))
		return fmt.Errorf("主动中止")
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("出错时不应写文件")
	}
	if st.Snapshot().MustGet("a") != json.Number("1") {
		t.Fatal("出错时内存不应改变")
	}
}

func TestSnapshotStableAfterUpdate(t *testing.T) {
	st, _ := Open(writeDB(t, `{"posts": [{"id": 1}]}`))
	snap := st.Snapshot()
	_ = st.Update(func(db *jsonx.Object) error {
		arr, _ := jsonx.AsArray(db.MustGet("posts"))
		db.Set("posts", append(arr, jsonx.Value("new")))
		return nil
	})
	arr, _ := jsonx.AsArray(snap.MustGet("posts"))
	if len(arr) != 1 {
		t.Fatalf("旧快照应不受更新影响: %v", arr)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	path := writeDB(t, `{"posts": []}`)
	st, _ := Open(path)
	var wg sync.WaitGroup
	const workers, each = 8, 25
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				rec := jsonx.NewObject()
				rec.Set("id", json.Number(strconv.Itoa(w*100+j)))
				err := st.Update(func(db *jsonx.Object) error {
					arr, _ := jsonx.AsArray(db.MustGet("posts"))
					db.Set("posts", append(arr, jsonx.Value(rec)))
					return nil
				})
				if err != nil {
					t.Errorf("更新失败: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	arr, _ := jsonx.AsArray(st.Snapshot().MustGet("posts"))
	if len(arr) != workers*each {
		t.Fatalf("并发更新丢失: %d", len(arr))
	}
	data, _ := os.ReadFile(path)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("文件应为合法 JSON: %v", err)
	}
	var posts []json.RawMessage
	if err := json.Unmarshal(doc["posts"], &posts); err != nil || len(posts) != workers*each {
		t.Fatalf("文件应与内存一致: %v", err)
	}
	// 不应留下临时文件
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jsonsrv-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

func TestReloadExternalChangeAndBad(t *testing.T) {
	path := writeDB(t, `{"a": 1}`)
	st, _ := Open(path)

	touch(path, `{"a": 2}`)
	changed, err := st.Reload()
	if err != nil || !changed {
		t.Fatalf("外部修改应重新加载: %v %v", changed, err)
	}
	if st.Snapshot().MustGet("a") != json.Number("2") {
		t.Fatal("内容应更新")
	}

	unchanged, err := st.Reload()
	if err != nil || unchanged {
		t.Fatal("未变化时应返回 false")
	}

	touch(path, `broken {`)
	changed, err = st.Reload()
	if err == nil || changed {
		t.Fatal("坏文件应报错且不替换")
	}
	if st.Snapshot().MustGet("a") != json.Number("2") {
		t.Fatal("坏文件应保留旧数据")
	}
	// 同一份坏内容不应反复报错
	if changed, err := st.Reload(); err != nil || changed {
		t.Fatal("同一份坏文件应避免重复报错")
	}

	touch(path, `{"a": 3}`)
	if changed, err := st.Reload(); err != nil || !changed {
		t.Fatal("修复后应可加载")
	}
}

func TestWatcherReloads(t *testing.T) {
	path := writeDB(t, `{"a": 1}`)
	st, _ := Open(path)
	changes := make(chan struct{}, 8)
	errs := make(chan error, 8)

	w := NewWatcher(st, 10*time.Millisecond)
	done := make(chan struct{})
	defer func() { close(done) }()
	ctx, cancel := contextWithCancel()
	defer cancel()
	_ = done
	recov := make(chan struct{}, 8)
	// onRecovered 回调：错误去重 + 恢复提示。
	w.Start(ctx,
		func() { changes <- struct{}{} },
		func(err error) { errs <- err },
		func() { recov <- struct{}{} })

	touch(path, `{"a": 2}`)
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher 应在限定时间内重新加载")
	}
	if st.Snapshot().MustGet("a") != json.Number("2") {
		t.Fatal("watcher 未生效")
	}

	touch(path, `bad json`)
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("错误回调不应为 nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("坏文件应触发错误回调")
	}
	if st.Snapshot().MustGet("a") != json.Number("2") {
		t.Fatal("坏文件应保留旧数据")
	}

	// 去重：仍处于错误态时再次改写坏文件，不应再报第二次错误。
	touch(path, `still bad`)
	select {
	case err := <-errs:
		t.Fatalf("错误态下应去重，不再报告第二次错误（收到 %v）", err)
	case <-time.After(300 * time.Millisecond):
	}

	// 恢复：文件恢复合法后应恰好报告一次恢复。
	touch(path, `{"a": 3}`)
	select {
	case <-recov:
	case <-time.After(3 * time.Second):
		t.Fatal("文件恢复后应触发恢复回调")
	}
	select {
	case <-recov:
		t.Fatal("恢复回调也应去重，只报告一次")
	case err := <-errs:
		t.Fatalf("恢复后不应再有错误: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if st.Snapshot().MustGet("a") != json.Number("3") {
		t.Fatal("恢复后应重新加载新数据")
	}
}

// touch 以可控的方式改写文件，并把 mtime 设定到未来，确保签名一定变化。
// 写入采用"临时文件 + rename"的原子替换：watcher 轮询不会读到截断中的文件，
// 否则去重/恢复断言会出现与产品无关的随机失败。
func touch(path, content string) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		panic(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(tmp, future, future); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		panic(err)
	}
}

// contextWithCancel 返回可取消的 context。
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
