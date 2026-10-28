//go:build darwin

package update

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// applyFileIcon restores the app's Finder icon on a service binary that was replaced.
// The icon files are kept beside the binary; a bare executable carries its icon in a
// resource fork, and Login Items shows it instead of the generic executable icon.
func applyFileIcon(path string) {
	dir := filepath.Dir(path)
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
