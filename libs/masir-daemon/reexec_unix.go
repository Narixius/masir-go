//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// reexec replaces this process with the binary at path, run with args (args[0] is the
// program name) and this environment. The PID stays the same, which launchd and
// systemd both accept.
func reexec(path string, args []string) error {
	return syscall.Exec(path, args, os.Environ())
}
