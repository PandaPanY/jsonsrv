//go:build windows

package store

import "os"

// fileOwner 在 Windows 上没有 POSIX 属主，统一返回不可用。
func fileOwner(fi os.FileInfo) (uid, gid int, ok bool) {
	return 0, 0, false
}
