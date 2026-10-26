//go:build windows

package main

import (
	"errors"
	"fmt"

	"os/exec"
	"os/user"
	"syscall"

	"golang.org/x/sys/windows"
)

// elevateDaemon starts the daemon as Administrator for this session, serving this account,
// through Windows' permission prompt. It takes over from the daemon running as the user.
// Not a service: it lasts until it stops or Windows restarts; Settings installs the service.
func elevateDaemon(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to start the daemon from")
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(bin)
	params, _ := windows.UTF16PtrFromString("daemon run --user " + windows.EscapeArg(u.Uid))
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("permission wasn't granted")
		}
		return fmt.Errorf("run as Administrator: %w", err)
	}
	return nil
}

// installService installs the daemon as a Windows service, so TUN needs no prompt from then
// on. masir asks for the permission itself, and waits for the outcome.
func installService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to install the service from")
	}
	cmd := exec.Command(bin, "daemon", "enable")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't install the service: %s", string(out))
	}
	return nil
}
