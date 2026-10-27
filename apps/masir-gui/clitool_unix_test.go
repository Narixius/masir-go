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
	oldDirs := cliStandardDirs
	cliStandardDirs = nil
	t.Cleanup(func() { cliStandardDirs = oldDirs })
	t.Setenv("PATH", filepath.Join(home, "empty"))
	t.Setenv("SHELL", "/bin/zsh")
	return home
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func markerCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), cliBlockStart)
}

func TestInstallCommandLineLinksTheBundledMasir(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "Masir.app", "Contents", "Resources", "bin", "masir")
	writeExecutable(t, bin)

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
	if !st.installed || !st.ours || !st.onPath || !st.needsShell {
		t.Fatalf("status = %+v, want installed, ours, on the PATH and needing a new shell", st)
	}
	zshrc := filepath.Join(home, ".zshrc")
	if markerCount(t, zshrc) != 1 {
		t.Fatalf("no managed block in %s", zshrc)
	}

	// Installing again changes nothing, and does not add a second block.
	st2, err := installCLI(bin)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if st2.needsShell {
		t.Fatalf("the second install claimed it changed the shell: %+v", st2)
	}
	if markerCount(t, zshrc) != 1 {
		t.Fatalf("the block was duplicated: %d", markerCount(t, zshrc))
	}

	// With the directory on the PATH, the command is found.
	t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
	if st := cliStatusNow(bin); !st.installed || !st.ours || !st.onPath {
		t.Fatalf("with the PATH set, status = %+v", st)
	}

	if err := removeCLI(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link is still there: %v", err)
	}
	if markerCount(t, zshrc) != 0 {
		t.Fatalf("the block is still there")
	}
	if st := cliStatusNow(bin); st.installed {
		t.Fatalf("still installed: %+v", st)
	}
}

func TestInstallCommandLineLeavesAnotherMasirAlone(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	other := filepath.Join(home, "other", "masir")
	writeExecutable(t, bin)
	writeExecutable(t, other)
	link := filepath.Join(home, ".local", "bin", "masir")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	st, err := installCLI(bin)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !st.installed || st.ours {
		t.Fatalf("status = %+v, want the other masir reported", st)
	}
	if target, err := os.Readlink(link); err != nil || !samePath(target, other) {
		t.Fatalf("the other link moved: %q, %v", target, err)
	}
	if err := removeCLI(bin); err == nil {
		t.Fatal("removed another masir's command")
	}
	if target, err := os.Readlink(link); err != nil || !samePath(target, other) {
		t.Fatalf("the other link moved: %q, %v", target, err)
	}
}

func TestInstallCommandLineLeavesAnExistingMasirOnThePath(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	otherDir := filepath.Join(home, "bin")
	other := filepath.Join(otherDir, "masir")
	writeExecutable(t, other)
	t.Setenv("PATH", otherDir)

	st, err := installCLI(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !st.installed || st.ours || !st.onPath {
		t.Fatalf("status = %+v, want the other masir on the PATH", st)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "masir")); !os.IsNotExist(err) {
		t.Fatalf("a second link was made: %v", err)
	}
	if markerCount(t, filepath.Join(home, ".zshrc")) != 0 {
		t.Fatalf("the shell config was changed for an existing CLI")
	}
}

func TestInstallCommandLineLeavesABrokenForeignLink(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	link := filepath.Join(home, ".local", "bin", "masir")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "other", "masir")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(bin); err == nil {
		t.Fatal("replaced a broken link to another program")
	}
	if target, err := os.Readlink(link); err != nil || target != other {
		t.Fatalf("the link moved: %q, %v", target, err)
	}
}

func TestInstallCommandLineRepairsABrokenLink(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "Masir.app", "Contents", "Resources", "bin", "masir")
	writeExecutable(t, bin)
	link := filepath.Join(home, ".local", "bin", "masir")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, "old", "Masir.app", "Contents", "Resources", "bin", "masir")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}

	st, err := installCLI(bin)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || !samePath(target, bin) {
		t.Fatalf("the link was not repaired: %q, %v", target, err)
	}
	if !st.installed || !st.ours {
		t.Fatalf("status = %+v", st)
	}
}

func TestInstallCommandLineKeepsAnExistingPathLine(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("export PATH=\"$HOME/.local/bin:$PATH\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := installCLI(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !st.installed || !st.onPath || st.needsShell {
		t.Fatalf("status = %+v", st)
	}
	if markerCount(t, zshrc) != 0 {
		t.Fatalf("a second PATH entry was added")
	}
	if !strings.Contains(string(mustRead(t, zshrc)), ".local/bin") {
		t.Fatalf("the person's PATH line was lost")
	}
}

func TestInstallCommandLineAddsBashAndFishPath(t *testing.T) {
	t.Run("bash", func(t *testing.T) {
		home := tempHome(t)
		t.Setenv("SHELL", "/bin/bash")
		bin := filepath.Join(home, "app", "bin", "masir")
		writeExecutable(t, bin)
		profile := filepath.Join(home, ".bash_profile")
		if err := os.WriteFile(profile, []byte("# profile\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		st, err := installCLI(bin)
		if err != nil {
			t.Fatal(err)
		}
		if !st.onPath {
			t.Fatalf("status = %+v", st)
		}
		for _, name := range []string{".bashrc", ".bash_profile"} {
			if markerCount(t, filepath.Join(home, name)) != 1 {
				t.Fatalf("no block in %s", name)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".profile")); !os.IsNotExist(err) {
			t.Fatalf("wrote .profile beside a bash login file: %v", err)
		}
		if err := removeCLI(bin); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".bashrc", ".bash_profile"} {
			if markerCount(t, filepath.Join(home, name)) != 0 {
				t.Fatalf("the block stayed in %s", name)
			}
		}
	})

	t.Run("fish", func(t *testing.T) {
		home := tempHome(t)
		t.Setenv("SHELL", "/usr/bin/fish")
		bin := filepath.Join(home, "app", "bin", "masir")
		writeExecutable(t, bin)

		st, err := installCLI(bin)
		if err != nil {
			t.Fatal(err)
		}
		if !st.onPath {
			t.Fatalf("status = %+v", st)
		}
		conf := filepath.Join(home, ".config", "fish", "conf.d", "masir.fish")
		body := string(mustRead(t, conf))
		if !strings.Contains(body, "set -gx PATH") {
			t.Fatalf("the fish path block is missing: %q", body)
		}
		if err := removeCLI(bin); err != nil {
			t.Fatal(err)
		}
		if markerCount(t, conf) != 0 {
			t.Fatalf("the block stayed in %s", conf)
		}
	})
}

func TestInstallCommandLineAddsBashLoginFileWhenBashrcIsNotSourced(t *testing.T) {
	home := tempHome(t)
	t.Setenv("SHELL", "/bin/bash")
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	profile := filepath.Join(home, ".bash_profile")
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(profile, []byte("# profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bashrc, []byte("export PATH=\"$HOME/.local/bin:$PATH\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := installCLI(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !st.installed || !st.onPath {
		t.Fatalf("status = %+v", st)
	}
	if markerCount(t, bashrc) != 0 {
		t.Fatalf("the .bashrc line was duplicated")
	}
	if markerCount(t, profile) != 1 {
		t.Fatalf("the login file did not get a block")
	}
}

func TestCLIStatusFindsAnotherMasir(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	otherDir := filepath.Join(home, "bin")
	writeExecutable(t, filepath.Join(otherDir, "masir"))

	if st := cliStatusNow(bin); !st.installed || st.ours || st.onPath {
		t.Fatalf("status without a PATH = %+v", st)
	}
	t.Setenv("PATH", otherDir)
	if st := cliStatusNow(bin); !st.installed || st.ours || !st.onPath {
		t.Fatalf("status with the PATH = %+v", st)
	}
}

func TestRemoveCLIKeepsAUserPathLine(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	zshrc := filepath.Join(home, ".zshrc")
	own := "# my own line\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# mine again\n"
	if err := os.WriteFile(zshrc, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(bin); err != nil {
		t.Fatal(err)
	}
	if markerCount(t, zshrc) != 0 {
		t.Fatalf("the installer added a second block")
	}
	if err := removeCLI(bin); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, zshrc)); got != own {
		t.Fatalf("the person's config changed:\n%s", got)
	}
}

func TestRemoveCLIOnlyRemovesItsBlock(t *testing.T) {
	home := tempHome(t)
	bin := filepath.Join(home, "app", "bin", "masir")
	writeExecutable(t, bin)
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# keep before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCLI(bin); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(zshrc, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# keep after\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	if err := removeCLI(bin); err != nil {
		t.Fatal(err)
	}
	body := string(mustRead(t, zshrc))
	if markerCount(t, zshrc) != 0 {
		t.Fatalf("the block stayed: %q", body)
	}
	for _, keep := range []string{"# keep before", "# keep after"} {
		if !strings.Contains(body, keep) {
			t.Fatalf("lost %q: %q", keep, body)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
