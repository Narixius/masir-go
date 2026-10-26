package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFriendlyUpdateError(t *testing.T) {
	for raw, want := range map[string]string{
		"install /usr/local/bin/masir: open /usr/local/bin/masir.new: permission denied": "permission",
		"install /x: read-only file system":                                              "read-only",
		"install /x: no space left on device":                                            "disk is full",
		"install /x: text file busy":                                                     "in use",
		"something else":                                                                 "something else",
	} {
		headline, adv := friendlyUpdateError(raw)
		if got := headline + " " + adv.plain(); !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("friendlyUpdateError(%q) = %q; missing %q", raw, got, want)
		}
	}
}

func TestSyncSelfCommandIsCopyable(t *testing.T) {
	src, dst := "/tmp/new masir/masir", "/usr/local/bin/masir"
	got := syncSelfCommand(src, dst)
	if runtime.GOOS == "windows" {
		if strings.Contains(got, "sudo") || !strings.Contains(got, `"`+dst+`"`) {
			t.Fatalf("windows command = %q", got)
		}
		return
	}
	for _, want := range []string{"sudo ", dst, "daemon sync-self", "--from", src, "--to"} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q; missing %q", got, want)
		}
	}
	if strings.Contains(got, "cp ") {
		t.Errorf("command should not use cp: %q", got)
	}
}

func TestWritableDir(t *testing.T) {
	if !writableDir(t.TempDir()) {
		t.Fatal("a temp dir should be writable")
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if writableDir(filepath.Clean(dir)) {
		t.Fatal("a read-only dir should not be writable")
	}
}
