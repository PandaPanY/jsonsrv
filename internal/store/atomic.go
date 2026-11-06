package store

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// defaultFilePerm 是新建数据文件时的缺省权限（尽力模拟 umask 022 的效果）。
const defaultFilePerm os.FileMode = 0o644

// permAndOwner 提取 FileInfo 的权限与属主（属主不可用时返回 -1）。
func permAndOwner(fi os.FileInfo) (perm os.FileMode, uid, gid int) {
	if u, g, ok := fileOwner(fi); ok {
		return fi.Mode().Perm(), u, g
	}
	return fi.Mode().Perm(), -1, -1
}

// fileSig 是文件修改时间加大小的组合，用作"文件是否变化"的廉价判据。
type fileSig struct {
	modTime time.Time
	size    int64
}

// statSig 读取文件签名。
func statSig(path string) (fileSig, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileSig{}, err
	}
	return fileSig{modTime: fi.ModTime(), size: fi.Size()}, nil
}

// writeFileAtomicFunc 先写临时文件（名字含数据文件名）、Sync 后改名覆盖目标，
// 进程中途被杀也不会损坏原文件。
// 符号链接：写入链接的真实目标文件，链接本身保留；临时文件与目标
// 同目录，保证改名不跨文件系统。原文件的权限与属主尽力保留（属主失败忽略）。
// 内容由 write 回调流式写入（大库保存时不在内存中拼完整字节串）。
func writeFileAtomicFunc(path string, write func(io.Writer) error) error {
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	dir := filepath.Dir(target)

	perm := defaultFilePerm
	uid, gid := -1, -1
	if fi, err := os.Stat(target); err == nil {
		perm, uid, gid = permAndOwner(fi)
	}

	// 临时文件名带数据文件名（.jsonsrv-<数据文件名>-*.tmp），启动时可据此
	// 精确识别并清理属于本数据文件的崩溃残留，不误伤其他数据文件的临时文件。
	tmp, err := os.CreateTemp(dir, ".jsonsrv-"+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("cannot set temporary file permissions: %w", err)
	}
	// 流式写入必须带缓冲：逐段直接 write(2) 会放大成数十万次系统调用（
	// 10 万记录库里实测单次 POST 近 9 秒，加缓冲后降为百毫秒级）。
	bw := bufio.NewWriterSize(tmp, 64*1024)
	if err := write(bw); err != nil {
		cleanup()
		return err
	}
	if err := bw.Flush(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if uid >= 0 && gid >= 0 {
		_ = os.Chown(target, uid, gid) // 尽力保留属主（非 root 常失败，忽略）
	}
	// 尽力同步目录项，失败不影响正确性
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
