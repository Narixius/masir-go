//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// elevateDaemon starts the daemon as root for this session, serving this user, through
// the system's password prompt. It takes over from the daemon running as the user. Not
// a service: it lasts until it stops or the machine restarts; Settings installs the service.
func elevateDaemon(bin string) error {
	if bin == "" {
		return fmt.Errorf("no masir executable to start the daemon from")
	}
	script := fmt.Sprintf(`do shell script %s with administrator privileges`,
		appleScriptString(fmt.Sprintf("%s daemon run --user %d >/dev/null 2>&1 &", shellQuote(bin), os.Getuid())))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "-128") { // the person cancelled the prompt
			return fmt.Errorf("permission wasn't granted")
		}
		return fmt.Errorf("permission wasn't granted: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// installService installs the daemon as the system service, so TUN needs no prompt
// from then on.
func installService(bin string) error {
	script := fmt.Sprintf(`do shell script %s with administrator privileges`,
		appleScriptString(fmt.Sprintf("%s daemon enable --user %s", shellQuote(bin), strconv.Itoa(os.Getuid()))))
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't install the service: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// appleScriptString quotes s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
