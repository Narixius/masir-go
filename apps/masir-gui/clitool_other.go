//go:build !darwin && !linux && !windows

package main

import "errors"

func cliStatusNow(string) cliStatus { return cliStatus{} }
func cliCommand() string            { return "masir" }
func installCLI(string) (cliStatus, error) {
	return cliStatus{}, errors.New("installing the command-line tool isn't supported here")
}
func removeCLI(string) error {
	return errors.New("installing the command-line tool isn't supported here")
}
func repairCLI(string) {}
