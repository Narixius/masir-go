//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"syscall"
)

// rootControlled reports whether only root can change path: the file and every
// directory above it belong to root and aren't writable by anyone else. A root daemon
// replaces only such files itself; one in a user's directory could be steered by a link
// the user plants there.
func rootControlled(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for p := real; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
			return false
		}
		if filepath.Dir(p) == p {
			return true
		}
	}
}
