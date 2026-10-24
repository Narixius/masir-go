package main

import (
	"os"
	"os/exec"
)

// reexec starts the binary at path with this process's arguments; this one then exits.
// Windows can't replace a running process in place.
func reexec(path string, args []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
