//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureRootDir(t *testing.T) {
	me := os.Getuid()
	dir := filepath.Join(t.TempDir(), "run", "masir")
	if err := EnsureRootDir(dir, me, 0o755); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("created %v, %v; want mode 0755", fi, err)
	}
	if err := EnsureRootDir(dir, me, 0o755); err != nil {
		t.Fatalf("existing directory: %v", err)
	}
	if err := EnsureRootDir(dir, me+1, 0o755); err == nil {
		t.Error("accepted a directory owned by someone else")
	}
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRootDir(dir, me, 0o755); err == nil {
		t.Error("accepted a group-writable directory")
	}
}

func TestEnsureRootDirRefusesASymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "masir")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRootDir(link, os.Getuid(), 0o755); err == nil {
		t.Error("accepted a symlink")
	}
}

func TestSocketsListPrivilegedFirst(t *testing.T) {
	socks, err := Sockets()
	if err != nil || len(socks) != 2 {
		t.Fatalf("Sockets = %v, %v", socks, err)
	}
	if socks[0] != PrivilegedSocketPath(os.Getuid()) || filepath.Base(socks[1]) != "daemon.sock" {
		t.Fatalf("Sockets = %v", socks)
	}
}
