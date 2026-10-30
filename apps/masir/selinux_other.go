//go:build !linux

package main

// labelServiceBinary relabels the service binary for SELinux; only Linux has SELinux.
// Everywhere else the install is already correct as written.
func labelServiceBinary(string) error { return nil }

// selinuxEnforcing is false off Linux: no SELinux, nothing to enforce.
func selinuxEnforcing() bool { return false }
