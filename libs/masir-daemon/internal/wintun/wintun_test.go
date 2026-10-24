package wintun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Name)
	if err := ensure(dir, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v1" {
		t.Fatalf("wrote %q", got)
	}
	before, _ := os.Stat(path)
	if err := ensure(dir, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !os.SameFile(before, after) {
		t.Error("an identical copy must be left alone: it may be loaded")
	}
	if err := ensure(dir, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2" {
		t.Fatalf("a different copy should be replaced, got %q", got)
	}
	for _, leftover := range []string{path + ".new", path + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s left behind", filepath.Base(leftover))
		}
	}
	if err := ensure(dir, nil); err == nil {
		t.Error("a build without the driver must say so")
	}
}

// The embedded DLLs are the files from wintun.net, unmodified, as the license requires.
func TestEmbeddedFilesUnchanged(t *testing.T) {
	for file, size := range map[string]int64{"wintun_amd64.dll": 427552, "wintun_arm64.dll": 222488} {
		fi, err := os.Stat(file)
		if err != nil || fi.Size() != size {
			t.Errorf("%s: %v, size %d; want the wintun.net 0.14.1 build (%d bytes)", file, err, fi.Size(), size)
		}
	}
}
