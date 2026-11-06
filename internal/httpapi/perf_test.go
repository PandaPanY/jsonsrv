//go:build !race

// 性能与基准测试仅在无 race 构建下运行：
// race 检测器的开销会让断言失去意义。
package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// bigDB 构造 n 条形 posts 的库：id 数字、title、views（i 的函数）、author.name 轮流。
func bigDB(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"posts":[`)
	authors := []string{"alice", "bob", "carol"}
	for i := 1; i <= n; i++ {
		if i > 1 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"title":"post %d","views":%d,"author":{"name":"%s"},"tags":["t%d","common"]}`,
			i, i, (i*7919)%100000, authors[i%len(authors)], i%97)
	}
	sb.WriteString(`],"profile":{"name":"typicode"}}`)
	return sb.String()
}

// TestPerfFilterSortPage100k 条件+排序+分页查询应远小于 100ms。
func TestPerfFilterSortPage100k(t *testing.T) {
	app, _ := newTestApp(t, bigDB(100000), Options{})
	const q = "/posts?author.name=bob&_sort=-views&_page=2&_per_page=100"
	// 预热（编译/首建）
	if status, _, _ := doReq(app, "GET", q, ""); status != 200 {
		t.Fatalf("预热查询状态 %d", status)
	}
	const loops = 20
	start := time.Now()
	for i := 0; i < loops; i++ {
		status, _, body := doReq(app, "GET", q, "")
		if status != 200 {
			t.Fatalf("状态 %d", status)
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(dataOf(t, body)), &items); err != nil {
			t.Fatalf("解析: %v (%s)", err, firstN(body, 200))
		}
		if len(items) != 100 {
			t.Fatalf("分页大小不符: %d", len(items))
		}
	}
	avg := time.Since(start) / loops
	t.Logf("10 万条记录，条件+排序+分页：平均 %v", avg)
	if avg > 100*time.Millisecond {
		t.Errorf("平均查询 %v，应 <100ms", avg)
	}
}

// BenchmarkFilterSortPage 条件+排序+分页查询的基准测试。
func BenchmarkFilterSortPage(b *testing.B) {
	app, _ := newTestApp(b, bigDB(100000), Options{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doReq(app, "GET", "/posts?author.name=carol&_sort=-views&_page=500&_per_page=20", "")
	}
}
