//go:build !windows

package main

import "syscall"

// detached starts the daemon in its own session, so it outlives this command and its terminal.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
