//go:build windows

package userfs

import "io/fs"

// Pin does nothing on Windows yet: a profile folder is often a junction or a redirected
// known folder, which a pinned handle would refuse to follow.
func Pin(dir string, uid, gid int) error { return nil }

func hardLinked(fs.FileInfo) bool { return false }
