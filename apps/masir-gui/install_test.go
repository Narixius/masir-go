//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narixius/masir-go/libs/masir-core/service"
)

func autoTestApp(t *testing.T) (*app, string) {
	t.Helper()
	home := tempHome(t)
	t.Setenv("MASIR_CLI_INSTALL", "1")
	t.Setenv("MASIR_NO_CLI_INSTALL", "")
	t.Setenv("MASIR_APP_NAME", "")
	bin := filepath.Join(home, "Masir.app", "Contents", "Resources", "bin", "masir")
	writeExecutable(t, bin)
	a := newTestApp(ukServers())
	a.binary = bin
	a.cliFile = filepath.Join(home, "cli-install")
	return a, home
}

func TestAutoInstallCLILinksOnFirstLaunch(t *testing.T) {
	a, home := autoTestApp(t)
	a.autoInstallCLI()
	if !a.cli.installed || !a.cli.ours {
		t.Fatalf("status = %+v", a.cli)
	}
	link := filepath.Join(home, ".local", "bin", "masir")
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("no link: %v", err)
	}
	if markerCount(t, filepath.Join(home, ".zshrc")) != 1 {
		t.Fatalf("no PATH block")
	}
	if choice := a.cliChoice(); choice != "done" {
		t.Fatalf("choice = %q", choice)
	}

	// A later launch checks the status but does not add a second block.
	a.autoInstallCLI()
	if markerCount(t, filepath.Join(home, ".zshrc")) != 1 {
		t.Fatalf("the block was duplicated")
	}
	if !a.cli.installed || !a.cli.ours {
		t.Fatalf("status after the second launch = %+v", a.cli)
	}
}

func TestAutoInstallCLIRestoresAMissingPathBlock(t *testing.T) {
	a, home := autoTestApp(t)
	a.autoInstallCLI()
	zshrc := filepath.Join(home, ".zshrc")
	if markerCount(t, zshrc) != 1 {
		t.Fatalf("the first install wrote no block")
	}
	if err := os.Remove(zshrc); err != nil {
		t.Fatal(err)
	}
	a.autoInstallCLI()
	if markerCount(t, zshrc) != 1 {
		t.Fatalf("the missing PATH block was not restored")
	}
}

func TestAutoInstallCLIRespectsRemoval(t *testing.T) {
	a, home := autoTestApp(t)
	if err := os.MkdirAll(filepath.Dir(a.cliFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.cliFile, []byte("off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.autoInstallCLI()
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "masir")); !os.IsNotExist(err) {
		t.Fatalf("the command came back after removal: %v", err)
	}
	if strings.TrimSpace(a.cliChoice()) != "off" {
		t.Fatalf("choice = %q", a.cliChoice())
	}
}

func TestAutoInstallCLISkipsADevCopy(t *testing.T) {
	a, home := autoTestApp(t)
	t.Setenv("MASIR_CLI_INSTALL", "")
	t.Setenv("MASIR_APP_NAME", "MasirSmoke")
	a.autoInstallCLI()
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "masir")); !os.IsNotExist(err) {
		t.Fatalf("a dev copy installed the CLI: %v", err)
	}
	if a.cliChoice() != "" {
		t.Fatalf("a dev copy recorded a choice")
	}
}

// A service whose definition exists but whose process is stopped is not "Active", and
// the app offers to start it instead of quietly running a second daemon.
func TestSettingsShowsAStoppedServiceAndOffersToStartIt(t *testing.T) {
	f := ukServers()
	f.status.ServiceInstalled = true
	f.status.ServiceRunning = false
	f.status.ServiceEnabled = true
	a := newTestApp(f)
	readService = func() service.Info { return service.Info{Installed: true, Running: false, Enabled: true} }
	a.serviceChecked = time.Time{}
	a.openScreen(screenSettings)
	tt := tester(a)
	if !tt.HasText("Stopped") {
		t.Fatalf("no stopped badge: %q", tt.Texts())
	}
	if !tt.HasText("Start Service") {
		t.Fatalf("no start action: %q", tt.Texts())
	}
}
