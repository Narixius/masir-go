//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Linux asks for root through polkit: pkexec shows the desktop's own password dialog.
// The daemon itself runs as the user for proxy mode; only TUN and the service need this.

func elevateDaemon(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to start the daemon from")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	// The shell puts the daemon in the background, so pkexec returns as soon as the user
	// has granted permission, and the daemon outlives it.
	script := fmt.Sprintf("%s daemon run --user %s >/dev/null 2>&1 &", shellQuote(abs), strconv.Itoa(os.Getuid()))
	if _, err := pkexec("/bin/sh", "-c", script); err != nil {
		return err
	}
	return nil
}

// installService installs the daemon as the systemd service through pkexec, so TUN needs
// no prompt from then on.
func installService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to install the service from")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	if _, err := pkexec(abs, "daemon", "enable", "--user", strconv.Itoa(os.Getuid())); err != nil {
		return err
	}
	return nil
}

func uninstallService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to remove the service with")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	if _, err := pkexec(abs, "daemon", "disable"); err != nil {
		return err
	}
	return nil
}

// startService starts the installed systemd unit again. The app never starts a second
// daemon of its own while a service is installed.
func startService(bin string) error {
	if bin == "" {
		return errors.New("no masir executable to start the service with")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	_, err = pkexec(abs, "daemon", "restart")
	return err
}

// openLoginItems has no equivalent on Linux: systemd's enablement is the setting.
func openLoginItems() error { return nil }

// pkexec runs a command as root through the desktop's polkit prompt.
func pkexec(argv ...string) ([]byte, error) {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return nil, errors.New("this desktop has no pkexec to ask for root; run `sudo masir daemon enable` once")
	}
	cmd := exec.Command("pkexec", argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("root permission wasn't granted: %s", msg)
	}
	return out, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
