//go:build windows

package server

// rootControlled is false on Windows: the CLI elevates itself to replace its own copy.
func rootControlled(string) bool { return false }
