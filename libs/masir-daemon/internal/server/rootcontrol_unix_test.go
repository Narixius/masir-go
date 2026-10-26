//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
)

func TestRootControlled(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("everything in the temp directory belongs to root here")
	}
	if _, err := os.Stat("/bin/sh"); err == nil && !rootControlled("/bin/sh") {
		t.Error("/bin/sh should be root-controlled")
	}
	mine := filepath.Join(t.TempDir(), "masir")
	if err := os.WriteFile(mine, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if rootControlled(mine) {
		t.Error("a file in the user's directory is not root-controlled")
	}
	if rootControlled(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a missing file is not root-controlled")
	}
}

// A root daemon runs the new version from its staging directory to check it before
// installing, and RAM-backed run directories are often mounted noexec: staging must be
// beside the service's own copy of masir, not under the run directory.
func TestRootStagingIsBesideTheServiceCopyNotInTheRunDir(t *testing.T) {
	dirs := privilegedUpdateDirs(501, "masir")
	if len(dirs) != 4 || dirs[0] != service.InstallDir() {
		t.Fatalf("dirs = %v", dirs)
	}
	for _, d := range dirs {
		if strings.HasPrefix(d, paths.PrivilegedRunDir+"/") || d == paths.PrivilegedRunDir {
			t.Errorf("%s is under the run directory", d)
		}
	}
	if got := dirs[3]; got != filepath.Join(service.InstallDir(), "updates", "501", "masir") {
		t.Errorf("staging = %s", got)
	}
}
