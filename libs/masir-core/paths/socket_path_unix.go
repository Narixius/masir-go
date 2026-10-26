//go:build !windows

package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// PrivilegedRunDir holds what a root daemon keeps at run time: its sockets and logs. It
// belongs to root, so no user can plant a symlink in it; a user's own directory is the
// one place a root daemon must not put things.
const PrivilegedRunDir = "/var/run/masir"

// SocketPath returns the socket of a daemon running as the current user.
func SocketPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return SocketPathIn(dir), nil
}

// SocketPathIn is the socket inside a config dir from ConfigDir or ConfigDirOf.
func SocketPathIn(dir string) string { return filepath.Join(dir, "daemon.sock") }

// PrivilegedSocketPath is the socket of the root daemon serving uid.
func PrivilegedSocketPath(uid int) string {
	return filepath.Join(PrivilegedRunDir, strconv.Itoa(uid)+".sock")
}

// PrivilegedLogDir is where the root daemon serving uid keeps Xray's error log.
func PrivilegedLogDir(uid int) string {
	return filepath.Join(PrivilegedRunDir, strconv.Itoa(uid))
}

// Sockets lists where a daemon for the current user may answer: a root daemon's socket
// first, then the one of a daemon running as the user.
func Sockets() ([]string, error) {
	user, err := SocketPath()
	if err != nil {
		return nil, err
	}
	return []string{PrivilegedSocketPath(os.Getuid()), user}, nil
}

// EnsureRootDir makes dir exist, owned by owner, with nobody else able to write to it.
// A directory somebody else could have made, or a symlink, is refused.
func EnsureRootDir(dir string, owner int, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.Mkdir(dir, mode); err != nil {
			return err
		}
		if err := os.Chmod(dir, mode); err != nil { // not narrowed by the umask
			return err
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case !ok || int(st.Uid) != owner:
		return fmt.Errorf("%s isn't owned by uid %d", dir, owner)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%s is writable by others", dir)
	}
	return nil
}
