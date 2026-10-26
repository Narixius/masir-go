//go:build !windows

package userfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Pin makes every later call work inside dir, the served user's directory, which it
// creates if it's missing. dir must belong to uid: a symlink that turned it into
// somebody else's directory (/etc, say) is refused, and what a privileged daemon
// creates inside the user's tree is made through a handle, not by path.
func Pin(dir string, uid, gid int) error {
	root, err := openOwned(dir, uid, gid)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if pinned != nil {
		_ = pinned.root.Close()
	}
	pinned = &pin{dir: dir, root: root, uid: uid, gid: gid, chown: uid >= 0 && os.Geteuid() == 0}
	return nil
}

func openOwned(dir string, uid, gid int) (*os.Root, error) {
	// Start from the deepest directory that exists, and create the rest inside it.
	anc, missing := dir, []string{}
	for {
		if fi, err := os.Stat(anc); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			return nil, fmt.Errorf("no existing parent of %s", dir)
		}
		missing = append([]string{filepath.Base(anc)}, missing...)
		anc = parent
	}
	root, err := os.OpenRoot(anc)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(root, uid); err != nil {
		root.Close()
		return nil, fmt.Errorf("%s: %w", anc, err)
	}
	if len(missing) > 0 {
		rel := filepath.Join(missing...)
		chown := uid >= 0 && os.Geteuid() == 0
		if err := mkdirAllIn(root, rel, uid, gid, chown); err != nil {
			root.Close()
			return nil, err
		}
		sub, err := root.OpenRoot(rel)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = sub
	}
	if err := checkOwner(root, uid); err != nil {
		root.Close()
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return root, nil
}

// checkOwner requires the directory the root points at to belong to uid.
func checkOwner(root *os.Root, uid int) error {
	if uid < 0 {
		return nil
	}
	fi, err := root.Stat(".")
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("can't tell who owns it")
	}
	if int(st.Uid) != uid {
		return fmt.Errorf("owned by uid %d, not by the user the daemon serves (uid %d)", st.Uid, uid)
	}
	return nil
}

// hardLinked reports whether the file has other names.
func hardLinked(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
