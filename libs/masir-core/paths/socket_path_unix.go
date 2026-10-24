//go:build !windows

package paths

import "path/filepath"

// SocketPath returns the daemon's Unix socket location for the current user.
func SocketPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return SocketPathIn(dir), nil
}

// SocketPathIn is the socket inside a config dir from ConfigDir or ConfigDirOf.
func SocketPathIn(dir string) string { return filepath.Join(dir, "daemon.sock") }
