//go:build !windows

package client

import "syscall"

// detached starts the daemon in its own session, so it outlives its parent and terminal.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
