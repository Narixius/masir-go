// Package userfs reads and writes the files in the served user's masir directory.
//
// A privileged daemon works inside a directory its user controls, so the user could
// replace a path component with a symlink and steer the daemon's writes (or chown) onto
// a file of their choosing. Pin opens the directory once, checks that it belongs to the
// user, and from then on every call goes through that handle: names are resolved inside
// it, and a symlink or ".." leading out of it is refused.
//
// Without a pin (an unprivileged daemon, which has no rights the user lacks) the calls
// are plain file operations.
package userfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type pin struct {
	dir      string // the directory's path, as callers spell it
	root     *os.Root
	uid, gid int
	chown    bool // created files and directories are handed to uid:gid
}

var (
	mu     sync.RWMutex
	pinned *pin
)

// Unpin drops the pin. Tests use it; a daemon pins for its whole life.
func Unpin() {
	mu.Lock()
	defer mu.Unlock()
	if pinned != nil {
		_ = pinned.root.Close()
		pinned = nil
	}
}

func current() *pin {
	mu.RLock()
	defer mu.RUnlock()
	return pinned
}

// rel turns a path inside the pinned directory into a name relative to it.
func (p *pin) rel(path string) (string, error) {
	r, err := filepath.Rel(p.dir, path)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s", path, p.dir)
	}
	return r, nil
}

// Write replaces path with data, mode 0600 (masird's files hold credentials), so a
// reader sees the old content or the new, never a mix.
func Write(path string, data []byte) error {
	if p := current(); p != nil {
		return p.write(path, data)
	}
	return writePlain(path, data)
}

// MkdirAll creates dir and any missing parents, mode 0700 for dir itself.
func MkdirAll(dir string) error {
	if p := current(); p != nil {
		r, err := p.rel(dir)
		if err != nil {
			return err
		}
		return mkdirAllIn(p.root, r, p.uid, p.gid, p.chown)
	}
	return os.MkdirAll(dir, 0o700)
}

// ReadFile reads a regular file. Under a pin, a file with other hard links is refused:
// the user could otherwise link a file they can't read into their directory.
func ReadFile(path string) ([]byte, error) {
	p := current()
	if p == nil {
		return os.ReadFile(path)
	}
	r, err := p.rel(path)
	if err != nil {
		return nil, err
	}
	f, err := p.root.Open(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if hardLinked(fi) {
		return nil, fmt.Errorf("%s has other hard links", path)
	}
	return io.ReadAll(f)
}

// ReadDir lists a directory.
func ReadDir(path string) ([]fs.DirEntry, error) {
	p := current()
	if p == nil {
		return os.ReadDir(path)
	}
	r, err := p.rel(path)
	if err != nil {
		return nil, err
	}
	d, err := p.root.Open(r)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// Remove deletes a file or an empty directory.
func Remove(path string) error {
	p := current()
	if p == nil {
		return os.Remove(path)
	}
	r, err := p.rel(path)
	if err != nil {
		return err
	}
	return p.root.Remove(r)
}

// RemoveAll deletes path and everything under it; a missing path is not an error.
func RemoveAll(path string) error {
	p := current()
	if p == nil {
		return os.RemoveAll(path)
	}
	r, err := p.rel(path)
	if err != nil {
		return err
	}
	return p.root.RemoveAll(r)
}

func (p *pin) write(path string, data []byte) error {
	r, err := p.rel(path)
	if err != nil {
		return err
	}
	dir, base := filepath.Split(r)
	tmp := filepath.Join(dir, "."+base+"-"+randomSuffix())
	f, err := p.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer p.root.Remove(tmp) // no-op after a successful rename
	if p.chown {
		if err := f.Chown(p.uid, p.gid); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return p.root.Rename(tmp, r)
}

func writePlain(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
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

// mkdirAllIn creates the missing directories of rel inside root, mode 0700 for the last
// and 0755 for the ones above it, handing each it creates to uid:gid.
func mkdirAllIn(root *os.Root, rel string, uid, gid int, chown bool) error {
	if rel == "." || rel == "" {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	cur := ""
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := root.Stat(cur) // follows a symlink, but only to somewhere inside root
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%s is not a directory", cur)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		perm := os.FileMode(0o755)
		if i == len(parts)-1 {
			perm = 0o700
		}
		if err := root.Mkdir(cur, perm); err != nil {
			return err
		}
		if chown {
			if err := root.Lchown(cur, uid, gid); err != nil {
				return err
			}
		}
	}
	return nil
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
