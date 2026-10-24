//go:build !windows

package service

import (
	"os"
	"runtime"
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
