//go:build !windows

package main

import (
	"os"
	"syscall"
)

// preserveOwner keeps a replaced binary owned by whoever owned it before.
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
