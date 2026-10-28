//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/narixius/masir-go/libs/masir-core/service"
)

// Login Items shows a legacy daemon's executable icon. A bare binary gets it from a
// resource fork and the FinderInfo custom-icon flag, which updates must re-apply.
func TestApplyFileIconSetsTheServiceIcon(t *testing.T) {
	dir := t.TempDir()
	rsrc := bytes.Repeat([]byte{1, 2, 3, 4}, 16)
	info := make([]byte, 32)
	info[8] = 0x04 // kHasCustomIcon in the Finder flags
	if err := os.WriteFile(filepath.Join(dir, "AppIcon.rsrc"), rsrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "FinderInfo.bin"), info, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "Masir")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyFileIcon(bin, dir)
	for _, c := range []struct {
		attr string
		want []byte
	}{
		{"com.apple.ResourceFork", rsrc},
		{"com.apple.FinderInfo", info},
	} {
		buf := make([]byte, len(c.want)+16)
		n, err := unix.Getxattr(bin, c.attr, buf)
		if err != nil || !bytes.Equal(buf[:n], c.want) {
			t.Fatalf("%s = %x, %v; want %x", c.attr, buf[:n], err, c.want)
		}
	}
}

// The legacy Login Items row falls back to the executable's name; capitalize it.
func TestServiceBinaryIsNamedMasir(t *testing.T) {
	if got := filepath.Base(service.BinaryPath()); got != "Masir" {
		t.Fatalf("service binary = %q, want Masir", got)
	}
}
