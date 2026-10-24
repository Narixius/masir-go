// Package service names masird's OS service, shared by the daemon (to report whether
// it's installed) and the CLI (to install it).
package service

import (
	"os"
	"runtime"
)

// Label is the launchd label of masird's service on macOS.
const Label = "com.masir.masird"

// DefinitionPath is the launchd plist or systemd unit; empty where masird has no service yet.
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

func Installed() bool {
	p := DefinitionPath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
