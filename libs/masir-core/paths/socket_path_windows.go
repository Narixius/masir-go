//go:build windows

package paths

import (
	"errors"
	"os"
)

const pipe = `\\.\pipe\masir-daemon`

// SocketPath returns the daemon's Windows named pipe endpoint.
func SocketPath() (string, error) { return pipe, nil }

// SocketPathIn ignores dir: the pipe namespace is machine-wide.
func SocketPathIn(string) string { return pipe }

// PrivilegedSocketPath is the same pipe: its access list, not its name, keeps users out.
func PrivilegedSocketPath(int) string { return pipe }

// PrivilegedLogDir is empty: on Windows the daemon keeps Xray's log with the rest of its
// files.
func PrivilegedLogDir(int) string { return "" }

// Sockets lists the one pipe a daemon answers on.
func Sockets() ([]string, error) { return []string{pipe}, nil }

// PrivilegedRunDir is empty: Windows has no run directory; the pipe's access list does
// the job.
const PrivilegedRunDir = ""

// EnsureRootDir always fails: no caller reaches it on Windows (PrivilegedLogDir is empty there), and it must not pass for a safe directory.
func EnsureRootDir(string, int, os.FileMode) error {
	return errors.New("root-owned directories aren't used on Windows")
}
