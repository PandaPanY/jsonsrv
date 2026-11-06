package httpapi

// 并发创建不得损坏 id（查重与生成都要在写锁回调内完成）。
// 如果查重/生成用写锁外的快照，--legacy 下并发 POST 会产生重复 id，
// 同一显式 id 并发 POST 会出现多个 201。

import (
	"sync"
	"testing"
)

// getIDsIn 通过 GET 读取集合的 id 列表。
func getIDsIn(t *testing.T, app *App, path string) []string {
	t.Helper()
	_, _, body := doReq(app, "GET", path, "")
	return idsOf(t, body)
}

// postNConcurrent 并发发 n 个 POST /posts 并返回状态码列表。
func postNConcurrent(app *App, n int, body string) []int {
	var wg sync.WaitGroup
	codes := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s, _, _ := doReq(app, "POST", "/posts", body)
			codes[i] = s
		}(i)
	}
	close(start)
	wg.Wait()
	return codes
}

// countCode 统计状态码出现次数。
func countCode(codes []int, want int) int {
	n := 0
	for _, c := range codes {
		if c == want {
			n++
		}
	}
	return n
}

// TestConcurrentAutoIDLegacy --legacy 并发 POST：记录数正确且 id 全部唯一。
func TestConcurrentAutoIDLegacy(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{Legacy: true})
	codes := postNConcurrent(app, 100, `{"title":"race"}`)
	if got := countCode(codes, 201); got != 100 {
		t.Fatalf("POST 201 数 = %d，期望 100", got)
	}
	arr := getIDsIn(t, app, "/posts")
	if len(arr) != 102 {
		t.Fatalf("posts 记录数 = %d，期望 102（2+100）", len(arr))
	}
	seen := map[string]bool{}
	for _, id := range arr {
		if seen[id] {
			t.Fatalf("发现重复 id：%s（共 %d 条记录）", id, len(arr))
		}
		seen[id] = true
	}
}

// TestConcurrentAutoIDDefault 默认模式并发 POST：记录数正确、随机 id 唯一。
func TestConcurrentAutoIDDefault(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	codes := postNConcurrent(app, 100, `{"title":"race"}`)
	if got := countCode(codes, 201); got != 100 {
		t.Fatalf("POST 201 数 = %d，期望 100", got)
	}
	arr := getIDsIn(t, app, "/posts")
	if len(arr) != 102 {
		t.Fatalf("posts 记录数 = %d，期望 102（2+100）", len(arr))
	}
	seen := map[string]bool{}
	for _, id := range arr {
		if seen[id] {
			t.Fatalf("发现重复 id：%s（共 %d 条记录）", id, len(arr))
		}
		seen[id] = true
	}
}

// TestConcurrentExplicitIDDup 同一显式 id 并发 POST：恰好 1 个 201，其余 409。
func TestConcurrentExplicitIDDup(t *testing.T) {
	app, _ := newTestApp(t, sampleDB, Options{})
	codes := postNConcurrent(app, 40, `{"id":9,"title":"dup"}`)
	if got := countCode(codes, 201); got != 1 {
		t.Errorf("POST 201 数 = %d，期望恰好 1（409 数 = %d）", got, countCode(codes, 409))
	}
	if got := countCode(codes, 409); got != 39 {
		t.Errorf("POST 409 数 = %d，期望 39", got)
	}
	arr := getIDsIn(t, app, "/posts")
	if len(arr) != 3 {
		t.Errorf("posts 记录数 = %d，期望 3", len(arr))
	}
}
