//go:build darwin

package main

import (
	"fmt"
	"os/exec"
)

// openTerminalCommand opens Terminal and runs cmd in it, in a new window.
func openTerminalCommand(cmd string) error {
	script := fmt.Sprintf(`tell application "Terminal" to do script %q
tell application "Terminal" to activate`, cmd)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	return nil
}

// openFile opens path with the user's default application.
func openFile(path string) error { return exec.Command("open", path).Run() }
