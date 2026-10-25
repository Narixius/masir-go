//go:build windows

package update

// preserveOwner is Unix-only; Windows keeps the ACL of the directory.
func preserveOwner(_, _ string) {}
