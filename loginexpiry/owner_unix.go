//go:build unix

package loginexpiry

import (
	"os"
	"syscall"
)

// ownedByMe: the file's owner is this process's user. Compared as int64:
// no narrowing conversion of either id.
func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int64(st.Uid) == int64(os.Getuid())
}
