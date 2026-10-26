//go:build !darwin && !windows && !linux

package main

import "errors"

var errNoElevation = errors.New("installing the daemon as a service isn't supported here; run `masir daemon enable` once")

func elevateDaemon(string) error    { return errNoElevation }
func installService(string) error   { return errNoElevation }
func uninstallService(string) error { return errNoElevation }
