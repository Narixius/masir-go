package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"

	"github.com/narixius/masir-go/libs/masir-core/service"
)

// Restart=on-failure: a clean exit (Shutdown) leaves it stopped, a crash brings it back.
const unitTemplate = `[Unit]
Description=masir daemon
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s daemon run --user %d
Restart=on-failure

[Install]
WantedBy=multi-user.target
`

func serviceUnit(bin string, uid int) string {
	return fmt.Sprintf(unitTemplate, bin, uid)
}

func installService(bin string, uid int) error {
	if err := os.WriteFile(service.DefinitionPath(), []byte(serviceUnit(bin, uid)), 0o644); err != nil {
		return err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "enable", "masird.service"); err != nil {
		return err
	}
	return run("systemctl", "restart", "masird.service") // `enable --now` wouldn't restart an older running copy
}

func uninstallService() error {
	_ = exec.Command("systemctl", "disable", "--now", "masird.service").Run()
	if err := os.Remove(service.DefinitionPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return run("systemctl", "daemon-reload")
}

func restartService() error {
	return sudo("systemctl", "restart", "masird.service")
}
