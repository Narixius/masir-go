//go:build darwin

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// applyFileIcon gives a bare service binary the app's Finder icon. A legacy LaunchDaemon
// has no bundle for Login Items to read: the icon is the executable's own resource fork
// plus the FinderInfo custom-icon flag. The two files sit beside the binary (installed
// from the app's resources), so an update can put the icon back on the new binary.
func applyFileIcon(path, dir string) {
	rsrc, err := os.ReadFile(filepath.Join(dir, "AppIcon.rsrc"))
	if err != nil {
		return
	}
	info, err := os.ReadFile(filepath.Join(dir, "FinderInfo.bin"))
	if err != nil {
		return
	}
	_ = unix.Setxattr(path, "com.apple.ResourceFork", rsrc, 0)
	_ = unix.Setxattr(path, "com.apple.FinderInfo", info, 0)
}
