//go:build windows

package main

// preserveOwner is Unix-only; Windows keeps the ACL of the directory.
func preserveOwner(_, _ string) {}
