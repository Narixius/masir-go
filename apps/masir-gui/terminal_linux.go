//go:build linux

package main

import (
	"errors"
	"os/exec"
)

// errNoTerminal is returned when no terminal emulator is installed.
var errNoTerminal = errors.New("no terminal emulator found")

// openTerminalCommand opens the first terminal emulator it finds and runs cmd in it.
func openTerminalCommand(cmd string) error {
	type term struct {
		bin  string
		args []string
	}
	for _, t := range []term{
		{"x-terminal-emulator", []string{"-e", "sh", "-c", cmd}},
		{"gnome-terminal", []string{"--", "sh", "-c", cmd}},
		{"konsole", []string{"-e", "sh", "-c", cmd}},
		{"xfce4-terminal", []string{"-e", "sh -c " + cmd}},
		{"xterm", []string{"-e", "sh", "-c", cmd}},
	} {
		if _, err := exec.LookPath(t.bin); err == nil {
			return exec.Command(t.bin, t.args...).Start()
		}
	}
	return errNoTerminal
}

// openFile opens path with the user's default application.
func openFile(path string) error {
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return errNoOpener
	}
	return exec.Command("xdg-open", path).Start()
}

// errNoOpener is returned when nothing on the system can open a file.
var errNoOpener = errors.New("no xdg-open found")
