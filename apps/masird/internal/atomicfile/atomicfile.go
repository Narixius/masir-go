// Package atomicfile writes files so readers see the old or new content, never a mix.
package atomicfile

import (
	"os"
	"path/filepath"
)

var owner struct {
	set      bool
	uid, gid int
}

// SetOwner makes files and directories created from now on belong to uid:gid. A root
// daemon calls it once at startup so the served user's directory stays theirs.
func SetOwner(uid, gid int) {
	owner.set, owner.uid, owner.gid = true, uid, gid
}

// Write replaces path with data, mode 0600 (masird's files hold credentials).
func Write(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if owner.set {
		if err := tmp.Chown(owner.uid, owner.gid); err != nil {
			tmp.Close()
			return err
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// MkdirAll creates dir (mode 0700) and hands it to the owner, if one is set.
func MkdirAll(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if owner.set {
		return os.Chown(dir, owner.uid, owner.gid)
	}
	return nil
}
