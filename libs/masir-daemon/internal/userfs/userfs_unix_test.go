//go:build !windows

package userfs

import (
	"os"
	"path/filepath"
	"testing"
)

func pinTemp(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "masir")
	if err := Pin(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Unpin)
	return dir
}

func TestPinCreatesTheDirectoryAndWorksInsideIt(t *testing.T) {
	dir := pinTemp(t)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("directory = %v, %v", fi, err)
	}
	file := filepath.Join(dir, "sub", "a.json")
	if err := MkdirAll(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	if err := Write(file, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := Write(file, []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(file)
	if err != nil || string(got) != "two" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, err := ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 1 || entries[0].Name() != "a.json" {
		t.Fatalf("ReadDir = %v, %v (a temp file was left behind?)", entries, err)
	}
	if err := Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
}

func TestPinRefusesASymlinkLeadingOut(t *testing.T) {
	dir := pinTemp(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The user swaps a directory and a file for symlinks to somewhere else.
	if err := os.Symlink(outside, filepath.Join(dir, "subscriptions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		t.Error("read through a symlink to a file outside the directory")
	}
	if _, err := ReadDir(filepath.Join(dir, "subscriptions")); err == nil {
		t.Error("listed a directory outside through a symlink")
	}
	if err := Write(filepath.Join(dir, "subscriptions", "x.json"), []byte("x")); err == nil {
		t.Error("wrote through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "x.json")); err == nil {
		t.Error("a file appeared outside the directory")
	}
	// Writing over a symlink replaces the link; the target stays as it was.
	if err := Write(filepath.Join(dir, "settings.json"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(secret); string(got) != `{"a":1}` {
		t.Errorf("the symlink's target was written: %q", got)
	}
}

func TestPinRefusesAHardLinkedFile(t *testing.T) {
	dir := pinTemp(t)
	other := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(other, filepath.Join(dir, "linked.json")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := ReadFile(filepath.Join(dir, "linked.json")); err == nil {
		t.Error("read a hard-linked file")
	}
}

func TestPinRefusesPathsOutsideTheDirectory(t *testing.T) {
	dir := pinTemp(t)
	for _, p := range []string{filepath.Join(dir, "..", "x"), filepath.Join(filepath.Dir(dir), "x"), "/etc/hosts"} {
		if err := Write(p, []byte("x")); err == nil {
			t.Errorf("Write(%q) outside the directory succeeded", p)
		}
		if _, err := ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) outside the directory succeeded", p)
		}
	}
}

func TestPinRefusesSomebodyElsesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "masir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Pin(dir, os.Getuid()+1, os.Getgid()); err == nil {
		Unpin()
		t.Fatal("pinned a directory owned by another user")
	}
}

func TestUnpinnedCallsArePlainFileOperations(t *testing.T) {
	Unpin()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.json")
	if err := Write(file, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFile(file); err != nil || string(got) != "x" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
}
