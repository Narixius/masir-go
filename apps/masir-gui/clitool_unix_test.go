//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempHome gives the test a home of its own, and a PATH that does not find the tool.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := userHome
	userHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHome = old })
	t.Setenv("PATH", filepath.Join(home, "empty"))
	return home
}

func TestInstallCommandLineLinksTheBundledMasir(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "Masir.app", "Contents", "Resources", "bin", "masir")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := installCLI(bin)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "masir")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("no link at %s: %v", link, err)
	}
	if !samePath(target, bin) {
		t.Fatalf("the link points at %s, want %s", target, bin)
	}
	if !st.installed || st.onPath {
		t.Fatalf("status = %+v, want installed and not on the PATH", st)
	}

	// Installing again changes nothing, and does not fail.
	if _, err := installCLI(bin); err != nil {
		t.Fatalf("second install: %v", err)
	}

	// With the directory on the PATH, the command is found.
	t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
	if st := cliStatusNow(bin); !st.installed || !st.onPath {
		t.Fatalf("with the PATH set, status = %+v", st)
	}

	if err := removeCLI(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link is still there: %v", err)
	}
	if st := cliStatusNow(bin); st.installed {
		t.Fatalf("still installed: %+v", st)
	}
}

func TestInstallCommandLineLeavesAnotherMasirAlone(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	other := filepath.Join(home, "other", "masir")
	for _, p := range []string{bin, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, ".local", "bin", "masir")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(bin); err == nil {
		t.Fatal("replaced another masir")
	} else if !strings.Contains(err.Error(), "another masir") {
		t.Fatalf("error = %v", err)
	}
	if err := removeCLI(bin); err == nil {
		t.Fatal("removed another masir's command")
	}
	if target, err := os.Readlink(link); err != nil || !samePath(target, other) {
		t.Fatalf("the other link moved: %q, %v", target, err)
	}
}
