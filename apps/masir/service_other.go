//go:build !darwin && !linux && !windows

package main

func installService(string, string) error { return errNoService }

func uninstallService() error { return errNoService }

func restartService() error { return errNoService }
