//go:build !darwin && !linux && !windows

package main

import "errors"

// errNoTerminal is returned where the app doesn't know how to open a terminal.
var errNoTerminal = errors.New("opening a terminal isn't supported on this system")

// openTerminalCommand is unsupported here; the command can still be copied.
func openTerminalCommand(string) error { return errNoTerminal }

// openFile is unsupported here.
func openFile(string) error { return errNoTerminal }
