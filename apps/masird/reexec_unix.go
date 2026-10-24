//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reexec replaces this process with the binary at path, keeping its arguments and
// environment. The PID stays the same, which launchd and systemd both accept.
func reexec(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
