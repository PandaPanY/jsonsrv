//go:build !windows

package store

import (
	"os"
	"syscall"
)

// fileOwner 返回文件属主的 uid/gid（unix）。
func fileOwner(fi os.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
