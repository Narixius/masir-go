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
	return runDaemonCommand(bin, "enable")
}

// uninstallService removes the daemon's service; the daemon then runs in the background
// when masir needs it.
func uninstallService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to remove the service with")
	}
	return runDaemonCommand(bin, "disable")
}

// startService starts the installed Windows service again. The app never starts a second
// daemon of its own while a service is installed.
func startService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to start the service with")
	}
	return runDaemonCommand(bin, "restart")
}

// openLoginItems has no equivalent on Windows: the service's start type is the setting.
func openLoginItems() error { return nil }

// runDaemonCommand runs `masir daemon <cmd>`; masir asks Windows for permission itself and
// waits for the elevated copy to report its outcome.
func runDaemonCommand(bin, cmd string) error {
	c := exec.Command(bin, "daemon", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := c.CombinedOutput(); err != nil {
		msg := string(out)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("couldn't %s the service: %s", map[string]string{"enable": "install", "disable": "remove", "restart": "start"}[cmd], msg)
	}
	return nil
}
