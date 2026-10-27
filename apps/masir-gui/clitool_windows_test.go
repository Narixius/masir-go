//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathDirsExpandsBothForms(t *testing.T) {
	t.Setenv("MASIR_TEST_DIR", `C:\Users\test\Masir`)
	got := pathDirs(`%MASIR_TEST_DIR%\bin;"C:\Program Files\Masir\bin";$MASIR_TEST_DIR\other`)
	want := []string{
		`C:\Users\test\Masir\bin`,
		`C:\Program Files\Masir\bin`,
		`C:\Users\test\Masir\other`,
	}
	if len(got) != len(want) {
		t.Fatalf("pathDirs = %#v", got)
	}
	for i := range want {
		if !samePath(got[i], want[i]) {
			t.Fatalf("pathDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCLIStatusFindsMasirOnThePath(t *testing.T) {
	home := t.TempDir()
	old := userHome
	userHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHome = old })

	dir := filepath.Join(home, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cliCommand()), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "app", "bin", cliCommand())
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	st := cliStatusNow(bin)
	if !st.installed || st.ours || !st.onPath {
		t.Fatalf("status = %+v", st)
	}
	if !samePath(st.path, filepath.Join(dir, cliCommand())) {
		t.Fatalf("path = %s", st.path)
	}
}
