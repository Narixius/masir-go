package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"

	"github.com/narixius/masir-go/libs/masir-core/service"
)

// The service exits 0 only when asked to (Shutdown), so KeepAlive restarts it after a
// crash but leaves it stopped after `masir daemon stop`. RunAtLoad brings it up at boot.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>run</string>
    <string>--user</string>
    <string>%d</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>/var/log/masird.log</string>
  <key>StandardErrorPath</key><string>/var/log/masird.log</string>
</dict>
</plist>
`

func servicePlist(bin string, uid int) string {
	return fmt.Sprintf(plistTemplate, service.Label, bin, uid)
}

func installService(bin, user string) error {
	uid, err := strconv.Atoi(user)
	if err != nil {
		return fail("the service's user must be a uid, not %q", user)
	}
	_ = exec.Command("launchctl", "bootout", "system/"+service.Label).Run() // replace an earlier install
	if err := os.WriteFile(service.DefinitionPath(), []byte(servicePlist(bin, uid)), 0o644); err != nil {
		return err
	}
	return run("launchctl", "bootstrap", "system", service.DefinitionPath())
}

func uninstallService() error {
	_ = exec.Command("launchctl", "bootout", "system/"+service.Label).Run()
	if err := os.Remove(service.DefinitionPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func restartService() error {
	return sudo("launchctl", "kickstart", "-k", "system/"+service.Label)
}
