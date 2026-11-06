package store

// 原子保存必须保留原文件权限，且通过符号链接启动时
// 链接本身保留、写入更新链接目标。否则临时文件 0600 改名覆盖后权限
// 变 0600，符号链接被替换成普通文件，原目标不再更新。

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// bumpPosts 向 db 的 posts 集合追加一条记录（触发保存）。
func bumpPosts(st *Store) error {
	return st.Update(func(db *jsonx.Object) error {
		arr, _ := jsonx.AsArray(db.MustGet("posts"))
		v, err := jsonx.Parse([]byte(`{"id":99,"note":"written"}`))
		if err != nil {
			return err
		}
		db.Set("posts", append(arr, v))
		return nil
	})
}

func TestSavePreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不模拟 unix 权限")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "db.json")
	if err := os.WriteFile(path, []byte(`{"posts":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bumpPosts(st); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("写后权限 = %o，期望保持 644", got)
	}
	// 非默认权限也要保留
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bumpPosts(st); err != nil {
		t.Fatal(err)
	}
	if fi, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("写后权限 = %o，期望保持 600", got)
	}
}

func TestSaveKeepsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 符号链接需要特权")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(target, []byte(`{"posts":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	st, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := bumpPosts(st); err != nil {
		t.Fatal(err)
	}
	// 链接本身应仍是符号链接
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("写后 %s 不再是符号链接（mode=%v）", link, fi.Mode())
	}
	// 目标文件内容应已更新
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := countPostsInData(t, data); got != 1 {
		t.Fatalf("链接目标 posts 数 = %d，期望 1（内容未写入目标文件）", got)
	}
}

// countPostsInData 解析数据并返回 posts 记录数。
func countPostsInData(t *testing.T, data []byte) int {
	t.Helper()
	v, err := jsonx.Parse(data)
	if err != nil {
		t.Fatalf("解析: %v", err)
	}
	obj, _ := jsonx.AsObject(v)
	arr, _ := jsonx.AsArray(obj.MustGet("posts"))
	return len(arr)
}
