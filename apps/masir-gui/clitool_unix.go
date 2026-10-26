//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The command-line tool is a link in the user's own bin directory, so installing it needs
// no administrator: it runs the masir the app ships with, and a later update of the app
// carries it along.

func cliBinDir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func cliCommand() string { return "masir" }

// cliStatusNow reads the link and whether the command is on the PATH.
func cliStatusNow(bin string) cliStatus {
	st := cliStatus{}
	dir, err := cliBinDir()
	if err != nil {
		return st
	}
	st.dir = dir
	st.path = filepath.Join(dir, cliCommand())
	if _, err := os.Readlink(st.path); err == nil {
		st.installed = true
	} else if fileExists(st.path) {
		st.installed = true
	}
	if p, err := exec.LookPath(cliCommand()); err == nil {
		st.onPath = samePath(p, st.path) || samePath(p, bin)
	}
	return st
}

// installCLI links bin into ~/.local/bin, leaving a command that is not the app's alone.
func installCLI(bin string) (cliStatus, error) {
	if bin == "" {
		return cliStatus{}, errors.New("the app ships no masir executable to install")
	}
	dir, err := cliBinDir()
	if err != nil {
		return cliStatus{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cliStatus{}, err
	}
	link := filepath.Join(dir, cliCommand())
	if target, err := os.Readlink(link); err == nil {
		if samePath(target, bin) {
			return cliStatusNow(bin), nil
		}
		return cliStatus{}, fmt.Errorf("%s already links another masir; remove it first", link)
	} else if fileExists(link) {
		return cliStatus{}, fmt.Errorf("%s is another program; move it out of the way first", link)
	}
	if err := os.Symlink(bin, link); err != nil {
		return cliStatus{}, err
	}
	return cliStatusNow(bin), nil
}

// removeCLI removes the link, and only when it is one to this app's masir.
func removeCLI(bin string) error {
	dir, err := cliBinDir()
	if err != nil {
		return err
	}
	link := filepath.Join(dir, cliCommand())
	target, err := os.Readlink(link)
	if err != nil {
		return nil // not a link: nothing this app made
	}
	if !samePath(target, bin) {
		return fmt.Errorf("%s links another masir; leaving it alone", link)
	}
	return os.Remove(link)
}
