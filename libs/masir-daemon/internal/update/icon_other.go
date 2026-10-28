//go:build !darwin

package update

// applyFileIcon is macOS-only; a bare service binary has no Finder icon elsewhere.
func applyFileIcon(path string) {}
