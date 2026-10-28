//go:build !windows

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefinitionPath is the launchd plist or systemd unit; empty where there's no service yet.
func DefinitionPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/LaunchDaemons/" + Label + ".plist"
	case "linux":
		return "/etc/systemd/system/masird.service"
	default:
		return ""
	}
}

// InstallDir holds the service's own copy of masir: a root daemon must not run a binary
// the user can modify.
func InstallDir() string { return "/usr/local/lib/masir" }

// BinaryPath is the masir executable the service runs, and the file an update replaces.
// On macOS it is capitalized: Login Items falls back to the executable's name for a
// legacy daemon, so "Masir" is the name the person sees.
func BinaryPath() string {
	name := "masir"
	if runtime.GOOS == "darwin" {
		name = "Masir"
	}
	return filepath.Join(InstallDir(), name)
}

// Supported reports whether masir can install its daemon as a service on this OS.
func Supported() bool { return DefinitionPath() != "" }

func Installed() bool {
	p := DefinitionPath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// statusCache keeps the UI's status stream from starting a subprocess for every change.
// The answer changes only when the person or the service manager acts, and a few seconds
// of staleness is invisible.
var statusCache struct {
	mu   sync.Mutex
	at   time.Time
	info Info
}

const statusTTL = 2 * time.Second

// Status reports what the OS service manager knows. It never returns Running or Enabled
// for an OS without a service.
func Status() Info {
	statusCache.mu.Lock()
	defer statusCache.mu.Unlock()
	if !statusCache.at.IsZero() && time.Since(statusCache.at) < statusTTL {
		return statusCache.info
	}
	info := queryStatus()
	statusCache.at, statusCache.info = time.Now(), info
	return info
}

func queryStatus() Info {
	switch runtime.GOOS {
	case "darwin":
		return darwinStatus()
	case "linux":
		return linuxStatus()
	}
	return Info{}
}

// darwinStatus asks launchd about the job: print succeeds while it is loaded, and the
// state line says whether it is running. A user can also disable it in Login Items, which
// print-disabled shows.
func darwinStatus() Info {
	p := DefinitionPath()
	if p == "" {
		return Info{}
	}
	if _, err := os.Stat(p); err != nil {
		return Info{}
	}
	info := Info{Installed: true, Enabled: true}
	out, err := exec.Command("launchctl", "print", "system/"+Label).Output()
	info.Running = err == nil && strings.Contains(string(out), "state = running")
	if disabled, err := exec.Command("launchctl", "print-disabled", "system").Output(); err == nil {
		if strings.Contains(string(disabled), `"`+Label+`" => disabled`) {
			info.Enabled = false
		}
	}
	return info
}

// linuxStatus asks systemd. is-active and is-enabled exit non-zero for inactive or
// disabled units, so their status is the answer.
func linuxStatus() Info {
	p := DefinitionPath()
	if p == "" {
		return Info{}
	}
	if _, err := os.Stat(p); err != nil {
		return Info{}
	}
	return Info{
		Installed: true,
		Running:   exec.Command("systemctl", "is-active", "--quiet", "masird.service").Run() == nil,
		Enabled:   exec.Command("systemctl", "is-enabled", "--quiet", "masird.service").Run() == nil,
	}
}
