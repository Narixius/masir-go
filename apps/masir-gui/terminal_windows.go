//go:build windows

package main

import "os/exec"

// openTerminalCommand opens PowerShell and runs cmd in it, leaving the window open.
func openTerminalCommand(cmd string) error {
	return exec.Command("cmd", "/c", "start", "", "powershell", "-NoExit", "-Command", cmd).Start()
}

// openFile opens path with the user's default application.
func openFile(path string) error {
	return exec.Command("cmd", "/c", "start", "", path).Start()
}
