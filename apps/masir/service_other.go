//go:build !darwin && !linux

package main

func installService(string, int) error { return errNoService }

func uninstallService() error { return errNoService }

func restartService() error { return errNoService }
