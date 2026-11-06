package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 崩溃（kill -9）后启动清理临时文件——临时文件命名带数据文件名，
// 启动时只删本数据文件的残留，其他数据文件的临时文件保留，并打印一条提示。

// TestStaleTempCleanupOnOpen 断言：主程序在端口监听成功后调用
// CleanupStaleTemps，严格匹配 .jsonsrv-<name>-<数字>.tmp；非纯数字后缀与其他数据文件
// 的临时文件保留，无关文件不受影响。
func TestStaleTempCleanupOnOpen(t *testing.T) {
	path := writeDB(t, `{"posts":[{"id":1}]}`)
	dir := filepath.Dir(path)
	mine := []string{
		".jsonsrv-db.json-12345.tmp",
		".jsonsrv-db.json-67890.tmp",
	}
	// 非纯数字后缀不再是本数据文件临时文件的严格命名，应保留
	patternKeep := ".jsonsrv-db.json-6789a.tmp"
	other := ".jsonsrv-other.json-000001.tmp"
	unrelated := "db.json.bak"
	for _, n := range append(mine, patternKeep, other, unrelated) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("half-written"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 清理已移出 Open，由主程序在端口监听成功后调用（本测试模拟）。
	cleanNote := st.CleanupStaleTemps()
	for _, n := range mine {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("本数据文件残留 %s 未被删除（err=%v）", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, other)); err != nil {
		t.Errorf("其他数据文件的临时文件被误删：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, patternKeep)); err != nil {
		t.Errorf("非纯数字后缀应保留（严格匹配）：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, unrelated)); err != nil {
		t.Errorf("无关文件被误删：%v", err)
	}
	if !strings.Contains(cleanNote, "removed 2 ") || !strings.Contains(cleanNote, mine[0]) {
		t.Errorf("清理提示不正确：%q", cleanNote)
	}
}

// TestTempNameCarriesDataFileName 断言 writeFileAtomicFunc 落盘期间的临时文件
// 名包含数据文件名（启动清理据此才能精确匹配本数据文件的残留）。
func TestTempNameCarriesDataFileName(t *testing.T) {
	path := writeDB(t, `{"a":1}`)
	var seen []string
	err := writeFileAtomicFunc(path, func(w io.Writer) error {
		ents, _ := os.ReadDir(filepath.Dir(path))
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".jsonsrv-") && strings.HasSuffix(e.Name(), ".tmp") {
				seen = append(seen, e.Name())
			}
		}
		_, err := w.Write([]byte(`{"a":2}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("落盘期间应恰好有 1 个临时文件，实际 %v", seen)
	}
	if !strings.HasPrefix(seen[0], ".jsonsrv-db.json-") {
		t.Errorf("临时文件名应包含数据文件名，实际为 %s", seen[0])
	}
}
