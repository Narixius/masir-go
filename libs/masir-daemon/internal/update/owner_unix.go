//go:build !windows

package update

import (
	"os"
	"syscall"
)

// preserveOwner keeps a replaced file owned by the user who owned it before, so a root
// daemon updating a user's copy doesn't leave it owned by root.
func preserveOwner(tmp, dst string) {
	fi, err := os.Stat(dst)
	if err != nil {
		return
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
}
