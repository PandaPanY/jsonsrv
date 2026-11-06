package query

import (
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// idsFrom 按当前顺序提取 id 列表。
func idsFrom(t *testing.T, docs []jsonx.Value) []string {
	t.Helper()
	var ids []string
	for _, d := range docs {
		o, ok := jsonx.AsObject(d)
		if !ok {
			t.Fatal("记录应为对象")
		}
		v, _ := o.Get("id")
		s, _ := jsonx.ToString(v)
		ids = append(ids, s)
	}
	return ids
}

// TestSortBasics 默认模式（v1 sortOn）：
// 字符串按小写归一比较（localeCompare 近似）、点号路径、"-" 前缀降序。
func TestSortBasics(t *testing.T) {
	docs := mustDocs(t, docsJSON)
	// name 升序（小写归一）：alice < bob < cara
	Sort(docs, "name")
	if got := idsFrom(t, docs); !eqList(got, []string{"1", "2", "3"}) {
		t.Errorf("name 升序: %v", got)
	}
	// meta.age 升序：25(2) < 30(1) < 40(3)
	Sort(docs, "meta.age")
	if got := idsFrom(t, docs); !eqList(got, []string{"2", "1", "3"}) {
		t.Errorf("meta.age 升序: %v", got)
	}
	// "-" 前缀降序：40, 30, 25
	Sort(docs, "-meta.age")
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2"}) {
		t.Errorf("meta.age 降序: %v", got)
	}
	// 多字段：先 name 再 id
	Sort(docs, "name,id")
	if got := idsFrom(t, docs); !eqList(got, []string{"1", "2", "3"}) {
		t.Errorf("多字段排序: %v", got)
	}
	// 空白 spec：保持原顺序
	docs = mustDocs(t, docsJSON)
	Sort(docs, " , , ")
	if got := idsFrom(t, docs); !eqList(got, []string{"1", "2", "3"}) {
		t.Errorf("空 spec 应保持不变: %v", got)
	}
}

// TestSortOnEmptyLast 对照 sortOn 的空值语义（实测 v→[4,1,3,2]）：
// null/""/false 挤到末尾（数字 0 不算空），空值之间保持输入顺序；
// 字符串按小写不区分比较。
func TestSortOnEmptyLast(t *testing.T) {
	docs := mustDocs(t, `[
	  {"id":1,"v":10},
	  {"id":2,"v":null},
	  {"id":3,"v":""},
	  {"id":4,"v":1}
	]`)
	Sort(docs, "v")
	// 实测（sortOn mixed v → [4,1,3,2]）：空值对之间顺序颠倒。
	if got := idsFrom(t, docs); !eqList(got, []string{"4", "1", "3", "2"}) {
		t.Errorf("空值应排在末尾: got %v", got)
	}
	// 缺失字段也算空值（排在数字之后，与 null 同样按颠倒处理）。
	docs = mustDocs(t, `[
	  {"id":1,"v":"b"},
	  {"id":2},
	  {"id":3,"v":null},
	  {"id":4,"v":"a"}
	]`)
	Sort(docs, "v")
	if got := idsFrom(t, docs); !eqList(got, []string{"4", "1", "3", "2"}) {
		t.Errorf("缺失应视为空值: got %v", got)
	}
	// 字符串小写归一：a < B。
	docs = mustDocs(t, `[{"id":1,"s":"B"},{"id":2,"s":"a"},{"id":3,"s":"A"}]`)
	Sort(docs, "s")
	if got := idsFrom(t, docs); !eqList(got, []string{"2", "3", "1"}) {
		t.Errorf("大小写归一: got %v", got)
	}
	// 数字 0 不算空值。
	docs = mustDocs(t, `[{"id":1,"v":0},{"id":2,"v":null},{"id":3,"v":-1}]`)
	Sort(docs, "v")
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2"}) {
		t.Errorf("数字 0 应参与正常比较: got %v", got)
	}
}

// TestSortLegacyOrderBy 对照 0.17 lodash orderBy（实测 v→[4,1,2,3]、s→[2,1,4,3]）：
// null/缺失放最后且相互稳定；字符串按字节序（大写在小写前）。
func TestSortLegacyOrderBy(t *testing.T) {
	docs := mustDocs(t, `[
	  {"id":1,"v":10},
	  {"id":2,"v":null},
	  {"id":3},
	  {"id":4,"v":1}
	]`)
	SortWithOrder(docs, "v", "", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"4", "1", "2", "3"}) {
		t.Errorf("orderBy null 最后稳定: got %v", got)
	}
	docs = mustDocs(t, `[
	  {"id":1,"s":"b"},
	  {"id":2,"s":"a"},
	  {"id":3,"s":null},
	  {"id":4,"s":"B"}
	]`)
	SortWithOrder(docs, "s", "", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"4", "2", "1", "3"}) {
		t.Errorf("orderBy 字节序: got %v", got)
	}
}

// TestSortWithOrder 0.17 _order 参数：逗号分隔按位置对应，非 "desc" 一律升序。
func TestSortWithOrder(t *testing.T) {
	docs := mustDocs(t, docsJSON)
	// _order=desc（legacy）：降序（同 "-meta.age"）
	SortWithOrder(docs, "meta.age", "desc", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2"}) {
		t.Errorf("_order=desc: %v", got)
	}
	// 大小写归一的 DESC
	SortWithOrder(docs, "meta.age", "DESC", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2"}) {
		t.Errorf("_order=DESC: %v", got)
	}
	// 其他值按升序
	SortWithOrder(docs, "meta.age", "asc", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"2", "1", "3"}) {
		t.Errorf("order=asc 应升序: %v", got)
	}
	// 位置多于字段时多余方向忽略；少于字段时缺省位置升序。
	docs = mustDocs(t, docsJSON)
	docs = append(docs, mustDocs(t, `[{"id":4,"meta":{"age":20}}]`)...)
	SortWithOrder(docs, "meta.age,id", "desc", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2", "4"}) {
		t.Errorf("orders 不足时缺省升序: got %v", got)
	}
	// legacy："-" 前缀不作为方向（取不到值 → 视为缺失放最后、稳定）。
	docs = mustDocs(t, docsJSON)
	SortWithOrder(docs, "-meta.age", "", true)
	if got := idsFrom(t, docs); !eqList(got, []string{"1", "2", "3"}) {
		t.Errorf("legacy '-' 应全为缺失保持原序: got %v", got)
	}
	// 默认模式：_order 按位置生效，与 0.17 一致（v1 beta 忽略它，不跟）。
	docs = mustDocs(t, docsJSON)
	SortWithOrder(docs, "meta.age", "desc", false)
	if got := idsFrom(t, docs); !eqList(got, []string{"3", "1", "2"}) {
		t.Errorf("默认模式 _order=desc 应按位置降序: got %v", got)
	}
}
