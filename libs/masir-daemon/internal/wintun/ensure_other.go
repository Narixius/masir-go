//go:build !windows

package wintun

// Ensure does nothing: only Windows needs Wintun.
func Ensure(string) error { return nil }
