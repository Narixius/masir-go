//go:build !windows

package main

import (
	"os"
	"strconv"
)

// privilegeName is what TUN needs, in the words of this OS.
const privilegeName = "root"

// elevationNote explains, before it happens, how masir gets privileges.
const elevationNote = "masir will install its daemon as a system service; sudo asks for your password once."

// elevationHint is how a privileged step announces itself.
const elevationHint = "sudo may ask for your password"

func elevated() bool { return os.Geteuid() == 0 }

// elevate re-runs masir with args as root, through sudo.
func elevate(args ...string) error { return asRoot(args...) }

// currentUser is who the service should serve: this user's uid.
func currentUser() string { return strconv.Itoa(os.Getuid()) }

// installDir holds the service's own copy of masir: a root daemon must not run a
// binary the user can modify.
func installDir() string { return "/usr/local/lib/masir" }

// stopServiceForInstall is a no-op: on Unix, a running binary can be renamed over.
func stopServiceForInstall() {}
