package store

// Store 访问器与 ParseData 的直接单测（断言既有行为）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorePathAndLoadNotes(t *testing.T) {
	path := writeDB(t, `{"a": 1}`)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Path() != path {
		t.Errorf("Path 应为 %s，得到 %s", path, st.Path())
	}
	if notes := st.LoadNotes(); len(notes) != 0 {
		t.Errorf("标准 JSON 无转换说明，得到 %v", notes)
	}

	// .json5 加载会产生"写回丢失注释"的转换说明（有意差异，README 已知差异同源）。
	p5 := filepath.Join(t.TempDir(), "db.json5")
	if err := os.WriteFile(p5, []byte("{ a: 1 /* 注释 */ }"), 0o644); err != nil {
		t.Fatal(err)
	}
	st5, err := Open(p5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(st5.LoadNotes(), "\n"), "JSON5") {
		t.Errorf("JSON5 加载应有写回说明，得到 %v", st5.LoadNotes())
	}
}

func TestParseData(t *testing.T) {
	doc, err := ParseData("db.json", []byte(`{"posts": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() != 1 {
		t.Errorf("应解析为顶层对象，得到 %v", doc)
	}
	if _, err := ParseData("db.json", []byte(`[1,2]`)); err == nil ||
		!strings.Contains(err.Error(), "db.json") {
		t.Errorf("顶层非对象应报错且带路径: %v", err)
	}
	if _, err := ParseData("db.json", []byte(`{"a"`)); err == nil {
		t.Error("截断 JSON 应报错")
	}
	if _, err := ParseData("db.json5", []byte("{ a: 1 }")); err != nil {
		t.Errorf("JSON5 宽松语法应通过: %v", err)
	}
	if _, err := ParseData("db.json5", []byte("{ a: }")); err == nil ||
		!strings.Contains(err.Error(), "db.json5") {
		t.Errorf("JSON5 语法错误应报错且带路径: %v", err)
	}
}
