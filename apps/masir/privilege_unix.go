//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
)

// privilegeName is what TUN needs, in the words of this OS.
const privilegeName = "root"

// elevationNote explains, before it happens, how masir gets privileges.
const elevationNote = "masir starts its daemon as root for this session; sudo asks for your password."

// enableTip says how to stop being asked, once a session daemon runs as root.
const enableTip = "Tip: `masir daemon enable` starts the daemon as root at boot, so connecting won't ask for your password again."

// elevationHint is how a privileged step announces itself.
const elevationHint = "sudo may ask for your password"

func elevated() bool { return os.Geteuid() == 0 }

// canElevateSilently reports whether becoming root won't prompt: already root, or sudo
// still remembers the password (or is set up not to ask).
func canElevateSilently() bool {
	return elevated() || exec.Command("sudo", "-n", "true").Run() == nil
}

// startElevatedDaemon starts `masir daemon run` as root in the background, serving
// user, its output going to log. It isn't a service: it lasts until it's stopped or
// the machine restarts. sudo asks for the password first, on this terminal, if needed.
func startElevatedDaemon(user string, log *os.File) error {
	exe, err := self()
	if err != nil {
		return err
	}
	argv := []string{exe, "daemon", "run", "--user", user}
	if !elevated() {
		auth := exec.Command("sudo", "-v")
		auth.Stdin, auth.Stdout, auth.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := auth.Run(); err != nil {
			return fail("root permission wasn't granted")
		}
		argv = append([]string{"sudo", "-n", "--"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// elevate re-runs masir with args as root, through sudo.
func elevate(args ...string) error { return asRoot(args...) }

// currentUser is who the service should serve: this user's uid.
func currentUser() string { return strconv.Itoa(os.Getuid()) }

// installDir holds the service's own copy of masir: a root daemon must not run a
// binary the user can modify.
func installDir() string { return "/usr/local/lib/masir" }

// stopServiceForInstall is a no-op: on Unix, a running binary can be renamed over.
func stopServiceForInstall() {}
