package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 临时文件清理不能误删——严格匹配 .jsonsrv-<数据文件名>-<纯数字>.tmp，
// 且清理动作放在端口监听成功之后（Open 不再清理，避免启动失败时误删）。

func TestStrictTempPatternCleanup(t *testing.T) {
	path := writeDB(t, `{"posts":[{"id":1}]}`)
	dir := filepath.Dir(path)
	deleteme := []string{
		".jsonsrv-db.json-111.tmp",
		".jsonsrv-db.json-222.tmp",
	}
	keep := []string{
		".jsonsrv-db.json-backup.json-222.tmp", // 同前缀的其他数据文件，不能被误删
		".jsonsrv-other.json-333.tmp",          // 其他数据文件
		".jsonsrv-db.json-6789a.tmp",           // 非纯数字后缀，严格模式不删
		".jsonsrv-db.json-444.tmp.bak",         // 不以 .tmp 结尾
	}
	for _, n := range append(deleteme, keep...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("half-written"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// 清理由主程序在端口监听成功后调用本方法（见 cmd/jsonsrv/main.go）。
	note := st.CleanupStaleTemps()
	for _, n := range deleteme {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("残留 %s 未被删除（err=%v）", n, err)
		}
	}
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s 被误删：%v", n, err)
		}
	}
	if !strings.Contains(note, "removed 2") {
		t.Errorf("清理提示不正确：%q", note)
	}
}

func TestOpenDoesNotCleanUp(t *testing.T) {
	path := writeDB(t, `{"a":1}`)
	tmp := filepath.Join(filepath.Dir(path), ".jsonsrv-db.json-555.tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("Open 不应清理残留（清理在监听成功后），err=%v", err)
	}
	for _, note := range st.LoadNotes() {
		if strings.Contains(note, "removed") {
			t.Errorf("清理提示不应进入 LoadNotes：%v", st.LoadNotes())
		}
	}
}

func TestSymlinkRealDirCleanup(t *testing.T) {
	realDir := t.TempDir()
	real := filepath.Join(realDir, "db.json")
	if err := os.WriteFile(real, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("文件系统不支持符号链接:", err)
	}
	tmp := filepath.Join(realDir, ".jsonsrv-db.json-777.tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	if note := st.CleanupStaleTemps(); note == "" {
		t.Error("真实目录下的残留未被清理")
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("真实目录残留未被删除")
	}
}
