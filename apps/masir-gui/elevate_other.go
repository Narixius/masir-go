//go:build !darwin && !windows

package main

import "errors"

var errNoElevation = errors.New("starting the daemon with elevated permission isn't supported here yet; run `masir daemon enable` once")

func elevateDaemon(string) error  { return errNoElevation }
func installService(string) error { return errNoElevation }
