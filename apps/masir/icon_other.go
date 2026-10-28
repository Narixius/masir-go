//go:build !darwin

package main

// applyFileIcon is macOS-only; the service binary has its own icon elsewhere.
func applyFileIcon(path, dir string) {}
